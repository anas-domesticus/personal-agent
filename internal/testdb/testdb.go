// Package testdb starts a throwaway pgvector Postgres for integration tests.
package testdb

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/anas-domesticus/personal-agent/internal/store"
)

// Open starts Postgres, migrates it and returns the store and its URL. The
// container is removed when the test ends.
func Open(t *testing.T) (*store.Store, string) {
	t.Helper()
	ctx := context.Background()

	ctr, err := postgres.Run(ctx, "pgvector/pgvector:0.8.7-pg17",
		postgres.WithDatabase("agent"),
		postgres.WithUsername("agent"),
		postgres.WithPassword("agent"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}

	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(st.Close)
	return st, url
}
