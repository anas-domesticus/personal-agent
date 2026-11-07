// Package catalog stores agents and workflows. Each has its own
// service, which validates input, wraps writes in transactions, queues
// embedding jobs where needed, and returns domain types.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/anas-domesticus/personal-agent/internal/domain"
)

// JobInserter queues River jobs inside a transaction. An insert-only River
// client satisfies it.
type JobInserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// namePattern is what agent and workflow names must look like.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func checkName(kind, name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%s name %q must be lowercase letters, digits and hyphens", kind, name)
	}
	return nil
}

func notFound(err error, what string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", what, domain.ErrNotFound)
	}
	return err
}

func uniqueViolation(err error, what string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%s: %w", what, domain.ErrExists)
	}
	return err
}

func foreignKeyViolation(err error, what string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return fmt.Errorf("%s: %w", what, domain.ErrConflict)
	}
	return err
}
