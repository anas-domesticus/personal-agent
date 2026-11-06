// Package store owns the Postgres connection, schema migrations and queries.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	pgxvec "github.com/pgvector/pgvector-go/pgx"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/anas-domesticus/personal-agent/internal/store/db"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps the connection pool and the generated queries.
type Store struct {
	*db.Queries
	Pool *pgxpool.Pool
}

// Open migrates the database to the latest schema, then connects and brings
// River's job tables up to date.
//
// Migrations run first, on their own connection, because the pool registers
// pgvector types on connect and those types only exist once the first
// migration has created the extension.
func Open(ctx context.Context, url string) (*Store, error) {
	if err := Migrate(ctx, url); err != nil {
		return nil, err
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return pgxvec.RegisterTypes(ctx, conn)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	if err := migrateRiver(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}

	return &Store{Queries: db.New(pool), Pool: pool}, nil
}

// InTx runs fn in a transaction, with queries bound to it. The transaction
// is also passed so jobs can be queued atomically with the writes.
func (s *Store) InTx(ctx context.Context, fn func(q *db.Queries, tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return fn(s.WithTx(tx), tx)
	})
}

// Close releases the connection pool.
func (s *Store) Close() { s.Pool.Close() }

// Migrate applies all pending migrations. A Postgres advisory lock makes it
// safe for several processes to start at once.
func Migrate(ctx context.Context, url string) error {
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		return fmt.Errorf("open migration connection: %w", err)
	}
	defer sqlDB.Close()

	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations,
		goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("migration provider: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	for _, r := range results {
		slog.Info("applied migration", "version", r.Source.Version, "file", r.Source.Path, "took", r.Duration)
	}
	return nil
}

// migrateRiver creates or upgrades River's job queue tables. River manages its
// own schema so that upgrading the River module upgrades its tables too.
func migrateRiver(ctx context.Context, pool *pgxpool.Pool) error {
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("river migrator: %w", err)
	}
	res, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("migrate river: %w", err)
	}
	for _, v := range res.Versions {
		slog.Info("applied river migration", "version", v.Version, "name", v.Name)
	}
	return nil
}
