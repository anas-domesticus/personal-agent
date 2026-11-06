package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/store"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

// ToolPolicy checks one entry of a version's "tools" config: that the tool
// exists, may be configured this way, and that its settings are valid.
// *tools.Registry implements it.
type ToolPolicy interface {
	Check(name string, value json.RawMessage) error
}

// Agents stores agents and their versions.
type Agents struct {
	st    *store.Store
	tools ToolPolicy
}

func NewAgents(st *store.Store, tools ToolPolicy) *Agents {
	return &Agents{st: st, tools: tools}
}

// validated is an domain.AgentVersionInput ready to store.
type validated struct {
	domain.AgentVersionInput
	config json.RawMessage
}

func (a *Agents) validate(in domain.AgentVersionInput, checkTools bool) (validated, error) {
	var errs []error
	if strings.TrimSpace(in.SystemPrompt) == "" {
		errs = append(errs, errors.New("system prompt is required"))
	}
	if p, m, ok := strings.Cut(in.Model, "/"); !ok || p == "" || m == "" {
		errs = append(errs, fmt.Errorf("model %q must be an OpenRouter slug like provider/model", in.Model))
	}
	cfg, err := domain.ParseAgentConfig(in.Config)
	if err != nil {
		errs = append(errs, err)
	} else if checkTools {
		for _, name := range sortedKeys(cfg.Tools) {
			if err := a.tools.Check(name, cfg.Tools[name]); err != nil {
				errs = append(errs, fmt.Errorf("config: tools.%s: %w", name, err))
			}
		}
	}
	if len(in.OutputSchema) > 0 {
		if _, err := workflow.CompileSchema(in.OutputSchema); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return validated{}, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return validated{}, err
	}
	return validated{AgentVersionInput: in, config: raw}, nil
}

// Create creates an agent with its first version, which becomes current.
// New agents are never admins.
func (a *Agents) Create(ctx context.Context, name, description string, in domain.AgentVersionInput) (domain.AgentVersion, error) {
	if err := checkName("agent", name); err != nil {
		return domain.AgentVersion{}, err
	}
	v, err := a.validate(in, true)
	if err != nil {
		return domain.AgentVersion{}, err
	}
	var out domain.AgentVersion
	err = a.st.InTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		row, err := q.CreateAgent(ctx, db.CreateAgentParams{Name: name, Description: description})
		if err != nil {
			return uniqueViolation(err, fmt.Sprintf("agent %q", name))
		}
		out, err = addAgentVersion(ctx, q, row.ID, v, true)
		return err
	})
	return out, err
}

// AddVersion appends a version. If activate is set it becomes current.
func (a *Agents) AddVersion(ctx context.Context, name string, in domain.AgentVersionInput, activate bool) (domain.AgentVersion, error) {
	v, err := a.validate(in, true)
	if err != nil {
		return domain.AgentVersion{}, err
	}
	var out domain.AgentVersion
	err = a.st.InTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		// Locking the agent serialises version numbering.
		row, err := q.GetAgentByNameForUpdate(ctx, name)
		if err != nil {
			return notFound(err, fmt.Sprintf("agent %q", name))
		}
		out, err = addAgentVersion(ctx, q, row.ID, v, activate)
		return err
	})
	return out, err
}

func addAgentVersion(ctx context.Context, q *db.Queries, id uuid.UUID, v validated, activate bool) (domain.AgentVersion, error) {
	var schema json.RawMessage
	if len(v.OutputSchema) > 0 {
		schema = v.OutputSchema
	}
	row, err := q.CreateAgentVersion(ctx, db.CreateAgentVersionParams{
		AgentID: id, SystemPrompt: v.SystemPrompt, Model: v.Model,
		Config: v.config, OutputSchema: schema, Notes: v.Notes,
	})
	if err != nil {
		return domain.AgentVersion{}, err
	}
	if activate {
		if err := q.SetAgentCurrentVersion(ctx, db.SetAgentCurrentVersionParams{ID: id, CurrentVersion: &row.Version}); err != nil {
			return domain.AgentVersion{}, err
		}
	}
	return toAgentVersion(row), nil
}

// SetVersion makes an existing version current (for rollback or promotion).
func (a *Agents) SetVersion(ctx context.Context, name string, version int32) error {
	return a.st.InTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		row, err := q.GetAgentByNameForUpdate(ctx, name)
		if err != nil {
			return notFound(err, fmt.Sprintf("agent %q", name))
		}
		if _, err := q.GetAgentVersion(ctx, db.GetAgentVersionParams{AgentID: row.ID, Version: version}); err != nil {
			return notFound(err, fmt.Sprintf("agent %q version %d", name, version))
		}
		return q.SetAgentCurrentVersion(ctx, db.SetAgentCurrentVersionParams{ID: row.ID, CurrentVersion: &version})
	})
}

// Get returns an agent and its current version.
func (a *Agents) Get(ctx context.Context, name string) (domain.AgentDetail, error) {
	row, err := a.st.GetAgentByName(ctx, name)
	if err != nil {
		return domain.AgentDetail{}, notFound(err, fmt.Sprintf("agent %q", name))
	}
	d := domain.AgentDetail{Agent: toAgent(row)}
	if row.CurrentVersion != nil {
		v, err := a.st.GetAgentVersion(ctx, db.GetAgentVersionParams{AgentID: row.ID, Version: *row.CurrentVersion})
		if err != nil {
			return domain.AgentDetail{}, err
		}
		cur := toAgentVersion(v)
		d.Current = &cur
	}
	return d, nil
}

func (a *Agents) List(ctx context.Context) ([]domain.Agent, error) {
	rows, err := a.st.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Agent, len(rows))
	for i, r := range rows {
		out[i] = toAgent(r)
	}
	return out, nil
}

// Versions lists an agent's versions, newest first.
func (a *Agents) Versions(ctx context.Context, name string) ([]domain.AgentVersion, error) {
	row, err := a.st.GetAgentByName(ctx, name)
	if err != nil {
		return nil, notFound(err, fmt.Sprintf("agent %q", name))
	}
	rows, err := a.st.ListAgentVersions(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.AgentVersion, len(rows))
	for i, r := range rows {
		out[i] = toAgentVersion(r)
	}
	return out, nil
}

// Ensure creates the agent with the given first version if it doesn't exist,
// and sets its admin flag. An existing agent's versions are left alone. It is
// safe for several processes to call at once.
func (a *Agents) Ensure(ctx context.Context, name, description string, admin bool, in domain.AgentVersionInput) error {
	// Seed versions skip the tool check: they come from code, and a tool
	// that isn't available in this process is skipped at build time anyway.
	v, err := a.validate(in, false)
	if err != nil {
		return fmt.Errorf("seed version for %q: %w", name, err)
	}
	return a.st.InTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		row, err := q.CreateAgentIfMissing(ctx, db.CreateAgentIfMissingParams{Name: name, Description: description})
		switch {
		case err == nil:
			if _, err := addAgentVersion(ctx, q, row.ID, v, true); err != nil {
				return err
			}
		case errors.Is(err, pgx.ErrNoRows):
			if row, err = q.GetAgentByName(ctx, name); err != nil {
				return err
			}
		default:
			return err
		}
		return q.SetAgentAdmin(ctx, db.SetAgentAdminParams{ID: row.ID, Admin: admin})
	})
}

func toAgent(r db.Agent) domain.Agent {
	return domain.Agent{
		ID: r.ID, Name: r.Name, Description: r.Description, Admin: r.Admin,
		CurrentVersion: r.CurrentVersion, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func toAgentVersion(r db.AgentVersion) domain.AgentVersion {
	// Stored configs were validated on write; read leniently so a removed
	// setting doesn't make old versions unreadable.
	var cfg domain.AgentConfig
	_ = json.Unmarshal(r.Config, &cfg)
	return domain.AgentVersion{
		AgentID: r.AgentID, Version: r.Version, SystemPrompt: r.SystemPrompt, Model: r.Model,
		Config: cfg, OutputSchema: r.OutputSchema, Notes: r.Notes, CreatedAt: r.CreatedAt,
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
