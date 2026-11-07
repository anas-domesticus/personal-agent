package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/catalog"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/embed"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
	"github.com/anas-domesticus/personal-agent/internal/testdb"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

func single(agent string) workflow.Graph {
	return workflow.Graph{Nodes: []workflow.Node{{ID: "a", Agent: agent}}}
}

func cyclic(agent string) workflow.Graph {
	return workflow.Graph{
		Nodes: []workflow.Node{{ID: "a", Agent: agent}, {ID: "b", Agent: agent}},
		Edges: []workflow.Edge{{From: "a", To: "b"}, {From: "b", To: "a"}},
	}
}

// isWorkflowEmbed matches the embed job queued for a workflow.
var isWorkflowEmbed = mock.MatchedBy(func(args river.JobArgs) bool {
	a, ok := args.(embed.Args)
	return ok && a.Target == embed.TargetWorkflow
})

func TestWorkflows(t *testing.T) {
	t.Parallel()
	st, _ := testdb.Open(t)
	ctx := context.Background()
	_, err := catalog.NewAgents(st, catalog.NewMockToolPolicy(t)).Create(ctx, "worker", "", version("p"))
	require.NoError(t, err)

	// Each case gets its own workflow name and mocks, so cases run in
	// parallel against the shared database.
	t.Run("create", func(t *testing.T) {
		t.Parallel()
		taken := catalog.NewWorkflows(st, okJobs(t), catalog.Search{})
		_, err := taken.Create(ctx, "taken", "", single("worker"), "")
		require.NoError(t, err)

		tests := []struct {
			name      string
			workflow  string
			graph     workflow.Graph
			jobs      func(t *testing.T) *catalog.MockJobInserter
			wantErr   string // substring; empty means no error
			wantErrIs error
		}{
			{name: "valid", workflow: "valid", graph: single("worker"), jobs: okJobs},
			{name: "bad name", workflow: "Bad Name", graph: single("worker"), wantErr: "must be lowercase letters, digits and hyphens"},
			{name: "name taken", workflow: "taken", graph: single("worker"), wantErrIs: domain.ErrExists},
			{name: "invalid graph", workflow: "empty-graph", graph: workflow.Graph{}, wantErr: "graph has no nodes"},
			{name: "cyclic graph", workflow: "cyclic", graph: cyclic("worker"), wantErr: "graph has a cycle"},
			{name: "unknown agent", workflow: "unknown-agent", graph: single("ghost"), wantErr: "unknown agents: ghost"},
			{name: "failed job insert rolls back", workflow: "no-jobs", graph: single("worker"),
				jobs: func(t *testing.T) *catalog.MockJobInserter {
					t.Helper()
					m := catalog.NewMockJobInserter(t)
					m.EXPECT().InsertTx(mock.Anything, mock.Anything, isWorkflowEmbed, mock.Anything).
						Return(nil, errors.New("queue down")).Once()
					return m
				},
				wantErr: "queue embed job: queue down"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				jobs := catalog.NewMockJobInserter(t) // no expectations: nothing is queued
				if tt.jobs != nil {
					jobs = tt.jobs(t)
				}
				workflows := catalog.NewWorkflows(st, jobs, catalog.Search{})

				v, err := workflows.Create(ctx, tt.workflow, "desc", tt.graph, "first")
				if tt.wantErr != "" || tt.wantErrIs != nil {
					if tt.wantErr != "" {
						require.ErrorContains(t, err, tt.wantErr)
					}
					if tt.wantErrIs != nil {
						require.ErrorIs(t, err, tt.wantErrIs)
						return
					}
					_, err := workflows.Get(ctx, tt.workflow)
					require.ErrorIs(t, err, domain.ErrNotFound, "a rejected workflow must not be stored")
					return
				}
				require.NoError(t, err)
				assert.Equal(t, int32(1), v.Version)
				assert.Equal(t, tt.graph, v.Graph)
				got, err := workflows.Get(ctx, tt.workflow)
				require.NoError(t, err)
				require.NotNil(t, got.Current)
				assert.Equal(t, int32(1), got.Current.Version)
			})
		}
	})

	t.Run("add version", func(t *testing.T) {
		t.Parallel()
		// No job expectations after Create: adding a version queues nothing.
		workflows := catalog.NewWorkflows(st, okJobs(t), catalog.Search{})
		_, err := workflows.Create(ctx, "versioned", "", single("worker"), "")
		require.NoError(t, err)

		// Ordered steps over one workflow, so they run sequentially.
		tests := []struct {
			name        string
			workflow    string
			graph       workflow.Graph
			activate    bool
			wantVersion int32
			wantCurrent int32
			wantErr     string // substring; empty means no error
			wantErrIs   error
		}{
			{name: "inactive version", workflow: "versioned", graph: single("worker"), wantVersion: 2, wantCurrent: 1},
			{name: "active version", workflow: "versioned", graph: single("worker"), activate: true, wantVersion: 3, wantCurrent: 3},
			{name: "unknown agent", workflow: "versioned", graph: single("ghost"), wantErr: "unknown agents: ghost"},
			{name: "cyclic graph", workflow: "versioned", graph: cyclic("worker"), wantErr: "graph has a cycle"},
			{name: "missing workflow", workflow: "ghost", graph: single("worker"), wantErrIs: domain.ErrNotFound},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				v, err := workflows.AddVersion(ctx, tt.workflow, tt.graph, "", tt.activate)
				switch {
				case tt.wantErr != "":
					require.ErrorContains(t, err, tt.wantErr)
					return
				case tt.wantErrIs != nil:
					require.ErrorIs(t, err, tt.wantErrIs)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, tt.wantVersion, v.Version)
				d, err := workflows.Get(ctx, tt.workflow)
				require.NoError(t, err)
				require.NotNil(t, d.CurrentVersion)
				assert.Equal(t, tt.wantCurrent, *d.CurrentVersion)
			})
		}

		all, err := workflows.Versions(ctx, "versioned")
		require.NoError(t, err)
		var numbers []int32
		for _, v := range all {
			numbers = append(numbers, v.Version)
		}
		assert.Equal(t, []int32{3, 2, 1}, numbers, "rejected versions aren't stored")
	})

	t.Run("delete", func(t *testing.T) {
		t.Parallel()
		workflows := catalog.NewWorkflows(st, okJobs(t), catalog.Search{})
		_, err := workflows.Create(ctx, "disposable", "", single("worker"), "")
		require.NoError(t, err)
		require.NoError(t, workflows.Delete(ctx, "disposable"))
		_, err = workflows.Get(ctx, "disposable")
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.ErrorIs(t, workflows.Delete(ctx, "disposable"), domain.ErrNotFound)

		withRun := catalog.NewWorkflows(st, okJobs(t), catalog.Search{})
		version, err := withRun.Create(ctx, "has-runs", "", single("worker"), "")
		require.NoError(t, err)
		_, err = st.CreateRun(ctx, db.CreateRunParams{
			WorkflowID: version.WorkflowID, WorkflowVersion: version.Version, Input: json.RawMessage(`{}`), TriggeredBy: "test",
		})
		require.NoError(t, err)
		require.ErrorIs(t, withRun.Delete(ctx, "has-runs"), domain.ErrConflict)
		_, err = withRun.Get(ctx, "has-runs")
		require.NoError(t, err)
	})
}

// okJobs expects one successful embed job insert.
func okJobs(t *testing.T) *catalog.MockJobInserter {
	t.Helper()
	m := catalog.NewMockJobInserter(t)
	m.EXPECT().InsertTx(mock.Anything, mock.Anything, isWorkflowEmbed, mock.Anything).
		Return(&rivertype.JobInsertResult{}, nil).Once()
	return m
}
