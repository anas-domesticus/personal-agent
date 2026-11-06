// Package runs owns workflow runs: queueing them, executing them as River
// jobs, recording each node as a step, and failing runs that were orphaned
// by a crash.
//
// Nodes run one at a time in dependency order. Each node runs its agent's
// version that is current when the node starts, and the step records which
// version that was. Data between nodes follows the contract in package
// workflow.
package runs

import (
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// Queue is the River queue workflow runs execute on.
const Queue = "runs"

// Timeout bounds a whole run.
const Timeout = 30 * time.Minute

// Args executes one run. The kind is stored in the job queue, so renaming it
// needs a migration of queued jobs.
type Args struct {
	RunID uuid.UUID `json:"run_id"`
}

func (Args) Kind() string { return "run_workflow" }

// Runs are not retried: a node may have had side effects, so running it again
// automatically isn't safe. A failed run is visible and can be started again.
func (Args) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: Queue, MaxAttempts: 1}
}

// reapArgs fails runs that can no longer finish.
type reapArgs struct{}

func (reapArgs) Kind() string { return "reap_runs" }

// Several processes may each schedule it; one per interval is enough.
func (reapArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: Queue, MaxAttempts: 1, UniqueOpts: river.UniqueOpts{ByPeriod: ReapInterval}}
}

// ReapInterval is how often orphaned runs are looked for.
const ReapInterval = 5 * time.Minute

// Register adds the run worker, the reaper and the runs queue to a River
// client config.
func Register(cfg *river.Config, w *Worker) {
	if cfg.Workers == nil {
		cfg.Workers = river.NewWorkers()
	}
	river.AddWorker(cfg.Workers, w)
	river.AddWorker(cfg.Workers, &reapWorker{store: w.Store})
	if cfg.Queues == nil {
		cfg.Queues = map[string]river.QueueConfig{}
	}
	cfg.Queues[Queue] = river.QueueConfig{MaxWorkers: 3}
	cfg.PeriodicJobs = append(cfg.PeriodicJobs, river.NewPeriodicJob(
		river.PeriodicInterval(ReapInterval),
		func() (river.JobArgs, *river.InsertOpts) { return reapArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true},
	))
}
