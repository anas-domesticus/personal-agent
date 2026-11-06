package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/embed"
	"github.com/anas-domesticus/personal-agent/internal/store"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

// Workflows stores workflows and their versions.
type Workflows struct {
	st     *store.Store
	jobs   JobInserter
	search Search
}

func NewWorkflows(st *store.Store, jobs JobInserter, search Search) *Workflows {
	return &Workflows{st: st, jobs: jobs, search: search}
}

// Create creates a workflow with its first version, which becomes current.
func (w *Workflows) Create(ctx context.Context, name, description string, graph workflow.Graph, notes string) (domain.WorkflowVersion, error) {
	if err := checkName("workflow", name); err != nil {
		return domain.WorkflowVersion{}, err
	}
	raw, err := w.checkGraph(ctx, graph)
	if err != nil {
		return domain.WorkflowVersion{}, err
	}
	var out domain.WorkflowVersion
	err = w.st.InTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		row, err := q.CreateWorkflow(ctx, db.CreateWorkflowParams{Name: name, Description: description})
		if err != nil {
			return uniqueViolation(err, fmt.Sprintf("workflow %q", name))
		}
		if out, err = addWorkflowVersion(ctx, q, row.ID, raw, notes, true); err != nil {
			return err
		}
		return w.queueEmbed(ctx, tx, row.ID)
	})
	return out, err
}

// AddVersion appends a version. If activate is set it becomes current.
func (w *Workflows) AddVersion(ctx context.Context, name string, graph workflow.Graph, notes string, activate bool) (domain.WorkflowVersion, error) {
	raw, err := w.checkGraph(ctx, graph)
	if err != nil {
		return domain.WorkflowVersion{}, err
	}
	var out domain.WorkflowVersion
	err = w.st.InTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		// Locking the workflow serialises version numbering.
		row, err := q.GetWorkflowByNameForUpdate(ctx, name)
		if err != nil {
			return notFound(err, fmt.Sprintf("workflow %q", name))
		}
		out, err = addWorkflowVersion(ctx, q, row.ID, raw, notes, activate)
		return err
	})
	return out, err
}

func addWorkflowVersion(ctx context.Context, q *db.Queries, id uuid.UUID, graph []byte, notes string, activate bool) (domain.WorkflowVersion, error) {
	row, err := q.CreateWorkflowVersion(ctx, db.CreateWorkflowVersionParams{WorkflowID: id, Graph: graph, Notes: notes})
	if err != nil {
		return domain.WorkflowVersion{}, err
	}
	if activate {
		if err := q.SetWorkflowCurrentVersion(ctx, db.SetWorkflowCurrentVersionParams{ID: id, CurrentVersion: &row.Version}); err != nil {
			return domain.WorkflowVersion{}, err
		}
	}
	return toWorkflowVersion(row)
}

// checkGraph validates structure and that every agent exists.
func (w *Workflows) checkGraph(ctx context.Context, g workflow.Graph) ([]byte, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	names := g.Agents()
	found, err := w.st.GetAgentsByNames(ctx, names)
	if err != nil {
		return nil, err
	}
	have := make(map[string]bool, len(found))
	for _, a := range found {
		have[a.Name] = true
	}
	var missing []string
	for _, n := range names {
		if !have[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("unknown agents: %s", strings.Join(missing, ", "))
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return nil, fmt.Errorf("encode graph: %w", err)
	}
	return raw, nil
}

// SetVersion makes an existing version current (for rollback or promotion).
func (w *Workflows) SetVersion(ctx context.Context, name string, version int32) error {
	return w.st.InTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		row, err := q.GetWorkflowByNameForUpdate(ctx, name)
		if err != nil {
			return notFound(err, fmt.Sprintf("workflow %q", name))
		}
		if _, err := q.GetWorkflowVersion(ctx, db.GetWorkflowVersionParams{WorkflowID: row.ID, Version: version}); err != nil {
			return notFound(err, fmt.Sprintf("workflow %q version %d", name, version))
		}
		return q.SetWorkflowCurrentVersion(ctx, db.SetWorkflowCurrentVersionParams{ID: row.ID, CurrentVersion: &version})
	})
}

// UpdateDescription changes the description and queues a new embedding.
func (w *Workflows) UpdateDescription(ctx context.Context, name, description string) error {
	return w.st.InTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		row, err := q.GetWorkflowByNameForUpdate(ctx, name)
		if err != nil {
			return notFound(err, fmt.Sprintf("workflow %q", name))
		}
		if err := q.UpdateWorkflowDescription(ctx, db.UpdateWorkflowDescriptionParams{ID: row.ID, Description: description}); err != nil {
			return err
		}
		return w.queueEmbed(ctx, tx, row.ID)
	})
}

func (w *Workflows) queueEmbed(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if _, err := w.jobs.InsertTx(ctx, tx, embed.Args{Target: embed.TargetWorkflow, ID: id}, nil); err != nil {
		return fmt.Errorf("queue embed job: %w", err)
	}
	return nil
}

// Get returns a workflow and its current version.
func (w *Workflows) Get(ctx context.Context, name string) (domain.WorkflowDetail, error) {
	row, err := w.st.GetWorkflowByName(ctx, name)
	if err != nil {
		return domain.WorkflowDetail{}, notFound(err, fmt.Sprintf("workflow %q", name))
	}
	d := domain.WorkflowDetail{Workflow: newWorkflow(row.ID, row.Name, row.Description, row.CurrentVersion, row.CreatedAt, row.UpdatedAt)}
	if row.CurrentVersion != nil {
		v, err := w.Version(ctx, row.ID, *row.CurrentVersion)
		if err != nil {
			return domain.WorkflowDetail{}, err
		}
		d.Current = &v
	}
	return d, nil
}

// Version returns one version of a workflow by id.
func (w *Workflows) Version(ctx context.Context, id uuid.UUID, version int32) (domain.WorkflowVersion, error) {
	row, err := w.st.GetWorkflowVersion(ctx, db.GetWorkflowVersionParams{WorkflowID: id, Version: version})
	if err != nil {
		return domain.WorkflowVersion{}, notFound(err, fmt.Sprintf("workflow %s version %d", id, version))
	}
	return toWorkflowVersion(row)
}

func (w *Workflows) List(ctx context.Context) ([]domain.Workflow, error) {
	rows, err := w.st.ListWorkflows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Workflow, len(rows))
	for i, r := range rows {
		out[i] = newWorkflow(r.ID, r.Name, r.Description, r.CurrentVersion, r.CreatedAt, r.UpdatedAt)
	}
	return out, nil
}

// Versions lists a workflow's versions, newest first.
func (w *Workflows) Versions(ctx context.Context, name string) ([]domain.WorkflowVersion, error) {
	row, err := w.st.GetWorkflowByName(ctx, name)
	if err != nil {
		return nil, notFound(err, fmt.Sprintf("workflow %q", name))
	}
	rows, err := w.st.ListWorkflowVersions(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.WorkflowVersion, len(rows))
	for i, r := range rows {
		if out[i], err = toWorkflowVersion(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Search finds workflows by meaning and keywords.
func (w *Workflows) Search(ctx context.Context, query string, limit int32) ([]domain.WorkflowMatch, error) {
	vec, semantic := w.search.vector(ctx, query)
	rows, err := w.st.SearchWorkflows(ctx, db.SearchWorkflowsParams{
		UseSemantic: semantic, Embedding: vec, Model: w.search.Model, Query: query, Candidates: limit * 4, Lim: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]domain.WorkflowMatch, len(rows))
	for i, r := range rows {
		out[i] = domain.WorkflowMatch{
			Workflow: domain.Workflow{ID: r.ID, Name: r.Name, Description: r.Description, CurrentVersion: r.CurrentVersion},
			Score:    r.Score,
		}
	}
	return out, nil
}

func newWorkflow(id uuid.UUID, name, desc string, current *int32, created, updated time.Time) domain.Workflow {
	return domain.Workflow{ID: id, Name: name, Description: desc, CurrentVersion: current, CreatedAt: created, UpdatedAt: updated}
}

func toWorkflowVersion(r db.WorkflowVersion) (domain.WorkflowVersion, error) {
	g, err := workflow.Parse(r.Graph)
	if err != nil {
		return domain.WorkflowVersion{}, fmt.Errorf("workflow %s version %d: stored graph is invalid: %w", r.WorkflowID, r.Version, err)
	}
	return domain.WorkflowVersion{WorkflowID: r.WorkflowID, Version: r.Version, Graph: g, Notes: r.Notes, CreatedAt: r.CreatedAt}, nil
}
