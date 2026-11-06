// Package runs defines the workflow run job and run statuses. It is a leaf
// package so both the catalog (which queues runs) and the runner (which
// executes them) can use it.
package runs

import (
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// Queue is the River queue workflow runs execute on.
const Queue = "runs"

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// Args executes one run.
type Args struct {
	RunID uuid.UUID `json:"run_id"`
}

func (Args) Kind() string { return "run_workflow" }

// Runs are not retried: a node may have had side effects, so running it again
// automatically isn't safe. A failed run is visible and can be started again.
func (Args) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: Queue, MaxAttempts: 1}
}

// Terminal reports whether a run has finished.
func Terminal(status string) bool {
	return status == StatusSucceeded || status == StatusFailed
}
