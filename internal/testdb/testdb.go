// Package testdb gives tests their own migrated pgvector Postgres database.
//
// The first Open in a test binary starts one container (testcontainers) and
// migrates a template database. Every Open then clones that template into a
// fresh database, which takes milliseconds, so tests are isolated from each
// other and can run in parallel. Testcontainers' reaper removes the container
// when the test binary exits.
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/anas-domesticus/personal-agent/internal/store"
)

const (
	image    = "pgvector/pgvector:0.8.7-pg17"
	template = "agent_template"
)

var (
	startOnce sync.Once
	adminURL  string // the container's maintenance database
	startErr  error

	// cloneMu serialises CREATE DATABASE ... TEMPLATE, which fails if another
	// clone of the same template is in progress.
	cloneMu sync.Mutex
	seq     atomic.Int64
)

// Open returns a store on a new, migrated database of its own, and the
// database's URL. The database is dropped when the test ends. It skips the
// test under -short (`task test:unit`), so unit runs need no Docker.
func Open(t *testing.T) (*store.Store, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("needs Docker; skipped with -short")
	}
	ctx := context.Background()

	startOnce.Do(func() { adminURL, startErr = start(ctx) })
	if startErr != nil {
		t.Fatalf("start postgres: %v", startErr)
	}

	name := fmt.Sprintf("test_%d", seq.Add(1))
	if err := exec(ctx, adminURL, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, template), &cloneMu); err != nil {
		t.Fatalf("create database: %v", err)
	}
	dbURL := withDatabase(adminURL, name)

	st, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		st.Close()
		if err := exec(context.Background(), adminURL, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name), nil); err != nil {
			t.Logf("drop database %s: %v", name, err)
		}
	})
	return st, dbURL
}

// start runs the container and migrates the template database.
func start(ctx context.Context) (string, error) {
	ctr, err := postgres.Run(ctx, image,
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("agent"),
		postgres.WithPassword("agent"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		return "", fmt.Errorf("run container: %w", err)
	}
	admin, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return "", fmt.Errorf("connection string: %w", err)
	}
	if err := exec(ctx, admin, "CREATE DATABASE "+template, nil); err != nil {
		return "", err
	}
	// Migrate, then close every connection: a template can't be cloned while
	// anyone is connected to it.
	if err := store.Migrate(ctx, withDatabase(admin, template)); err != nil {
		return "", fmt.Errorf("migrate template: %w", err)
	}
	return admin, nil
}

func exec(ctx context.Context, dbURL, sql string, mu *sync.Mutex) error {
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, sql); err != nil {
		return fmt.Errorf("%s: %w", sql, err)
	}
	return nil
}

func withDatabase(dbURL, name string) string {
	u, err := url.Parse(dbURL)
	if err != nil {
		panic(fmt.Sprintf("testdb: bad url %q: %v", dbURL, err))
	}
	u.Path = "/" + name
	return u.String()
}
