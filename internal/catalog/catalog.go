// Package catalog is the write and read path for agents, workflows and
// memories. It validates input, wraps writes in transactions, and queues
// embedding jobs in the same transaction so they can't be lost.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pgvector/pgvector-go"
	"github.com/riverqueue/river"

	"github.com/anas-domesticus/personal-agent/internal/embed"
	"github.com/anas-domesticus/personal-agent/internal/llm"
	"github.com/anas-domesticus/personal-agent/internal/store"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

// ErrNotFound is returned when a named agent, workflow or version doesn't exist.
var ErrNotFound = errors.New("not found")

const embeddingDims = 1536

type Catalog struct {
	store *store.Store
	jobs  *river.Client[pgx.Tx]

	// Embedder is optional. Without it, search is keyword-only.
	embedder embedding.Embedder

	// assignable reports whether an agent version may list a tool by name.
	// Admin tools are never assignable: they come only from the admin flag.
	assignable func(name string) bool
}

type Options struct {
	Store      *store.Store
	Jobs       *river.Client[pgx.Tx]
	Embedder   embedding.Embedder
	Assignable func(name string) bool
}

func New(o Options) *Catalog {
	return &Catalog{store: o.Store, jobs: o.Jobs, embedder: o.Embedder, assignable: o.Assignable}
}

// SetAssignable sets the tool name check. It exists because the tool
// registry is built from the catalog, so it can't be passed to New.
func (c *Catalog) SetAssignable(fn func(name string) bool) { c.assignable = fn }

// tx runs fn in a transaction with queries and the River client bound to it.
func (c *Catalog) tx(ctx context.Context, fn func(q *db.Queries, tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, c.store.Pool, func(tx pgx.Tx) error {
		return fn(c.store.WithTx(tx), tx)
	})
}

func (c *Catalog) queueEmbed(ctx context.Context, tx pgx.Tx, target embed.Target, id uuid.UUID) error {
	if _, err := c.jobs.InsertTx(ctx, tx, embed.Args{Target: target, ID: id}, nil); err != nil {
		return fmt.Errorf("queue embed job: %w", err)
	}
	return nil
}

func notFound(err error, what string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", what, ErrNotFound)
	}
	return err
}

func uniqueViolation(err error, what string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%s already exists", what)
	}
	return err
}

// --- Agents ------------------------------------------------------------------

type AgentVersionInput struct {
	SystemPrompt string
	Model        string
	Tools        []string
	Config       json.RawMessage // optional; defaults to {}
	OutputSchema json.RawMessage // optional JSON Schema object
	Notes        string
}

func (c *Catalog) validateAgentVersion(in *AgentVersionInput) error {
	var errs []error
	if strings.TrimSpace(in.SystemPrompt) == "" {
		errs = append(errs, errors.New("system prompt is required"))
	}
	if p, m, ok := strings.Cut(in.Model, "/"); !ok || p == "" || m == "" {
		errs = append(errs, fmt.Errorf("model %q must be an OpenRouter slug like provider/model", in.Model))
	}
	for _, t := range in.Tools {
		if c.assignable == nil || !c.assignable(t) {
			errs = append(errs, fmt.Errorf("tool %q does not exist or cannot be assigned", t))
		}
	}
	if len(in.Config) == 0 {
		in.Config = json.RawMessage(`{}`)
	}
	if !isJSONObject(in.Config) {
		errs = append(errs, errors.New("config must be a JSON object"))
	}
	if len(in.OutputSchema) > 0 {
		if _, err := workflow.CompileSchema(in.OutputSchema); err != nil {
			errs = append(errs, err)
		}
	}
	if in.Tools == nil {
		in.Tools = []string{}
	}
	return errors.Join(errs...)
}

// CreateAgent creates an agent with its first version, which becomes current.
// New agents are never admins.
func (c *Catalog) CreateAgent(ctx context.Context, name, description string, v AgentVersionInput) (db.AgentVersion, error) {
	if err := c.validateAgentVersion(&v); err != nil {
		return db.AgentVersion{}, err
	}
	var out db.AgentVersion
	err := c.tx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		a, err := q.CreateAgent(ctx, db.CreateAgentParams{Name: name, Description: description})
		if err != nil {
			return uniqueViolation(err, fmt.Sprintf("agent %q", name))
		}
		out, err = addAgentVersion(ctx, q, a.ID, v, true)
		return err
	})
	return out, err
}

// AddAgentVersion appends a version. If activate is set it becomes current.
func (c *Catalog) AddAgentVersion(ctx context.Context, name string, v AgentVersionInput, activate bool) (db.AgentVersion, error) {
	if err := c.validateAgentVersion(&v); err != nil {
		return db.AgentVersion{}, err
	}
	var out db.AgentVersion
	err := c.tx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		a, err := q.GetAgentByName(ctx, name)
		if err != nil {
			return notFound(err, fmt.Sprintf("agent %q", name))
		}
		out, err = addAgentVersion(ctx, q, a.ID, v, activate)
		return err
	})
	return out, err
}

func addAgentVersion(ctx context.Context, q *db.Queries, id uuid.UUID, v AgentVersionInput, activate bool) (db.AgentVersion, error) {
	var schema []byte
	if len(v.OutputSchema) > 0 {
		schema = v.OutputSchema
	}
	ver, err := q.CreateAgentVersion(ctx, db.CreateAgentVersionParams{
		AgentID: id, SystemPrompt: v.SystemPrompt, Model: v.Model, Tools: v.Tools,
		Config: v.Config, OutputSchema: schema, Notes: v.Notes,
	})
	if err != nil {
		return db.AgentVersion{}, err
	}
	if activate {
		if err := q.SetAgentCurrentVersion(ctx, db.SetAgentCurrentVersionParams{ID: id, CurrentVersion: &ver.Version}); err != nil {
			return db.AgentVersion{}, err
		}
	}
	return ver, nil
}

// SetAgentVersion makes an existing version current (for rollback or promotion).
func (c *Catalog) SetAgentVersion(ctx context.Context, name string, version int32) error {
	return c.tx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		a, err := q.GetAgentByName(ctx, name)
		if err != nil {
			return notFound(err, fmt.Sprintf("agent %q", name))
		}
		if _, err := q.GetAgentVersion(ctx, db.GetAgentVersionParams{AgentID: a.ID, Version: version}); err != nil {
			return notFound(err, fmt.Sprintf("agent %q version %d", name, version))
		}
		return q.SetAgentCurrentVersion(ctx, db.SetAgentCurrentVersionParams{ID: a.ID, CurrentVersion: &version})
	})
}

type AgentDetail struct {
	Agent   db.Agent
	Current *db.AgentVersion // nil if the agent has no current version
}

func (c *Catalog) GetAgent(ctx context.Context, name string) (AgentDetail, error) {
	a, err := c.store.GetAgentByName(ctx, name)
	if err != nil {
		return AgentDetail{}, notFound(err, fmt.Sprintf("agent %q", name))
	}
	d := AgentDetail{Agent: a}
	if a.CurrentVersion != nil {
		v, err := c.store.GetAgentVersion(ctx, db.GetAgentVersionParams{AgentID: a.ID, Version: *a.CurrentVersion})
		if err != nil {
			return AgentDetail{}, err
		}
		d.Current = &v
	}
	return d, nil
}

func (c *Catalog) ListAgents(ctx context.Context) ([]db.Agent, error) {
	return c.store.ListAgents(ctx)
}

func (c *Catalog) ListAgentVersions(ctx context.Context, name string) ([]db.AgentVersion, error) {
	a, err := c.store.GetAgentByName(ctx, name)
	if err != nil {
		return nil, notFound(err, fmt.Sprintf("agent %q", name))
	}
	return c.store.ListAgentVersions(ctx, a.ID)
}

// EnsureAgent creates the agent with the given first version if it doesn't
// exist, and sets its admin flag. An existing agent's versions are left alone.
func (c *Catalog) EnsureAgent(ctx context.Context, name, description string, admin bool, v AgentVersionInput) error {
	return c.tx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		a, err := q.GetAgentByName(ctx, name)
		if errors.Is(err, pgx.ErrNoRows) {
			if a, err = q.CreateAgent(ctx, db.CreateAgentParams{Name: name, Description: description}); err != nil {
				return err
			}
			// Seed versions bypass tool validation: admin tools come from the flag.
			if v.Config == nil {
				v.Config = json.RawMessage(`{}`)
			}
			if v.Tools == nil {
				v.Tools = []string{}
			}
			if _, err := addAgentVersion(ctx, q, a.ID, v, true); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		return q.SetAgentAdmin(ctx, db.SetAgentAdminParams{ID: a.ID, Admin: admin})
	})
}

// --- Workflows ---------------------------------------------------------------

// CreateWorkflow creates a workflow with its first version, which becomes current.
func (c *Catalog) CreateWorkflow(ctx context.Context, name, description string, graph workflow.Graph, notes string) (db.WorkflowVersion, error) {
	raw, err := c.checkGraph(ctx, graph)
	if err != nil {
		return db.WorkflowVersion{}, err
	}
	var out db.WorkflowVersion
	err = c.tx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		w, err := q.CreateWorkflow(ctx, db.CreateWorkflowParams{Name: name, Description: description})
		if err != nil {
			return uniqueViolation(err, fmt.Sprintf("workflow %q", name))
		}
		if out, err = addWorkflowVersion(ctx, q, w.ID, raw, notes, true); err != nil {
			return err
		}
		return c.queueEmbed(ctx, tx, embed.TargetWorkflow, w.ID)
	})
	return out, err
}

// AddWorkflowVersion appends a version. If activate is set it becomes current.
func (c *Catalog) AddWorkflowVersion(ctx context.Context, name string, graph workflow.Graph, notes string, activate bool) (db.WorkflowVersion, error) {
	raw, err := c.checkGraph(ctx, graph)
	if err != nil {
		return db.WorkflowVersion{}, err
	}
	var out db.WorkflowVersion
	err = c.tx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		w, err := q.GetWorkflowByName(ctx, name)
		if err != nil {
			return notFound(err, fmt.Sprintf("workflow %q", name))
		}
		out, err = addWorkflowVersion(ctx, q, w.ID, raw, notes, activate)
		return err
	})
	return out, err
}

func addWorkflowVersion(ctx context.Context, q *db.Queries, id uuid.UUID, graph []byte, notes string, activate bool) (db.WorkflowVersion, error) {
	v, err := q.CreateWorkflowVersion(ctx, db.CreateWorkflowVersionParams{WorkflowID: id, Graph: graph, Notes: notes})
	if err != nil {
		return db.WorkflowVersion{}, err
	}
	if activate {
		if err := q.SetWorkflowCurrentVersion(ctx, db.SetWorkflowCurrentVersionParams{ID: id, CurrentVersion: &v.Version}); err != nil {
			return db.WorkflowVersion{}, err
		}
	}
	return v, nil
}

// checkGraph validates structure and that every agent exists.
func (c *Catalog) checkGraph(ctx context.Context, g workflow.Graph) ([]byte, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	names := g.Agents()
	found, err := c.store.GetAgentsByNames(ctx, names)
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
	return json.Marshal(g)
}

func (c *Catalog) SetWorkflowVersion(ctx context.Context, name string, version int32) error {
	return c.tx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		w, err := q.GetWorkflowByName(ctx, name)
		if err != nil {
			return notFound(err, fmt.Sprintf("workflow %q", name))
		}
		if _, err := q.GetWorkflowVersion(ctx, db.GetWorkflowVersionParams{WorkflowID: w.ID, Version: version}); err != nil {
			return notFound(err, fmt.Sprintf("workflow %q version %d", name, version))
		}
		return q.SetWorkflowCurrentVersion(ctx, db.SetWorkflowCurrentVersionParams{ID: w.ID, CurrentVersion: &version})
	})
}

func (c *Catalog) UpdateWorkflowDescription(ctx context.Context, name, description string) error {
	return c.tx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		w, err := q.GetWorkflowByName(ctx, name)
		if err != nil {
			return notFound(err, fmt.Sprintf("workflow %q", name))
		}
		if err := q.UpdateWorkflowDescription(ctx, db.UpdateWorkflowDescriptionParams{ID: w.ID, Description: description}); err != nil {
			return err
		}
		return c.queueEmbed(ctx, tx, embed.TargetWorkflow, w.ID)
	})
}

type WorkflowDetail struct {
	Workflow db.GetWorkflowByNameRow
	Current  *db.WorkflowVersion
}

func (c *Catalog) GetWorkflow(ctx context.Context, name string) (WorkflowDetail, error) {
	w, err := c.store.GetWorkflowByName(ctx, name)
	if err != nil {
		return WorkflowDetail{}, notFound(err, fmt.Sprintf("workflow %q", name))
	}
	d := WorkflowDetail{Workflow: w}
	if w.CurrentVersion != nil {
		v, err := c.store.GetWorkflowVersion(ctx, db.GetWorkflowVersionParams{WorkflowID: w.ID, Version: *w.CurrentVersion})
		if err != nil {
			return WorkflowDetail{}, err
		}
		d.Current = &v
	}
	return d, nil
}

func (c *Catalog) ListWorkflows(ctx context.Context) ([]db.ListWorkflowsRow, error) {
	return c.store.ListWorkflows(ctx)
}

func (c *Catalog) ListWorkflowVersions(ctx context.Context, name string) ([]db.WorkflowVersion, error) {
	w, err := c.store.GetWorkflowByName(ctx, name)
	if err != nil {
		return nil, notFound(err, fmt.Sprintf("workflow %q", name))
	}
	return c.store.ListWorkflowVersions(ctx, w.ID)
}

func (c *Catalog) SearchWorkflows(ctx context.Context, query string, limit int32) ([]db.SearchWorkflowsRow, error) {
	vec, semantic, err := c.queryVector(ctx, query)
	if err != nil {
		return nil, err
	}
	return c.store.SearchWorkflows(ctx, db.SearchWorkflowsParams{
		UseSemantic: semantic, Embedding: vec, Query: query, Candidates: limit * 4, Lim: limit,
	})
}

// --- Memories ----------------------------------------------------------------

func (c *Catalog) Remember(ctx context.Context, content string, tags []string, source string) (uuid.UUID, error) {
	if strings.TrimSpace(content) == "" {
		return uuid.Nil, errors.New("content is required")
	}
	if tags == nil {
		tags = []string{}
	}
	var id uuid.UUID
	err := c.tx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		m, err := q.CreateMemory(ctx, db.CreateMemoryParams{
			Content: content, Tags: tags, Source: source, Metadata: json.RawMessage(`{}`),
		})
		if err != nil {
			return err
		}
		id = m.ID
		return c.queueEmbed(ctx, tx, embed.TargetMemory, m.ID)
	})
	return id, err
}

func (c *Catalog) SearchMemories(ctx context.Context, query string, tags []string, limit int32) ([]db.SearchMemoriesRow, error) {
	vec, semantic, err := c.queryVector(ctx, query)
	if err != nil {
		return nil, err
	}
	if tags == nil {
		tags = []string{}
	}
	return c.store.SearchMemories(ctx, db.SearchMemoriesParams{
		UseSemantic: semantic, Embedding: vec, Query: query, Tags: tags, Candidates: limit * 4, Lim: limit,
	})
}

// queryVector embeds a search query. Without an embedder it returns a
// placeholder vector and false, and search falls back to keywords.
func (c *Catalog) queryVector(ctx context.Context, query string) (pgvector.Vector, bool, error) {
	if c.embedder == nil {
		return pgvector.NewVector(make([]float32, embeddingDims)), false, nil
	}
	out, err := c.embedder.EmbedStrings(ctx, []string{query})
	if err != nil {
		return pgvector.Vector{}, false, fmt.Errorf("embed query: %w", err)
	}
	return pgvector.NewVector(llm.Float32s(out[0])), true, nil
}

func isJSONObject(raw []byte) bool {
	var m map[string]any
	return json.Unmarshal(raw, &m) == nil && m != nil
}
