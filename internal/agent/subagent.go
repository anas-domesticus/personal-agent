package agent

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

//go:embed subagent_prompt.md
var subagentPrompt string

// SubagentToolName is the subagent tool's name in an agent's tools config.
const SubagentToolName = "subagent"

// SubagentSettings configure an agent's subagent tool.
type SubagentSettings struct {
	// Models limits which models subagents may use. Empty allows any
	// OpenRouter model that supports tool calling.
	Models []string `json:"models,omitempty"`
	// MaxDepth is how many levels of subagents may be nested below this
	// agent. 0 means 1: subagents can't start their own.
	MaxDepth int `json:"max_depth,omitempty"`
}

func (s *SubagentSettings) Validate() error {
	if s.MaxDepth < 0 || s.MaxDepth > 3 {
		return errors.New("max_depth must be between 0 and 3")
	}
	return nil
}

func (s *SubagentSettings) depth() int {
	if s.MaxDepth == 0 {
		return 1
	}
	return s.MaxDepth
}

// SubagentSpec registers the subagent tool so versions can configure it.
// The builder makes the tool itself, since running one means building an
// agent.
func SubagentSpec() kit.Spec {
	return kit.Spec{
		Name:         SubagentToolName,
		Summary:      "Delegate a task to a subagent: a stored agent, or an ad-hoc worker on any OpenRouter model.",
		Settings:     func() any { return &SubagentSettings{} },
		SettingsHelp: `{"models": ["provider/model", ...] (allowed models; empty allows any with tool support), "max_depth": 1-3 (nesting levels allowed, default 1)}`,
	}
}

// SubagentStore keeps subagent conversations. *chat.PGStore implements it.
type SubagentStore interface {
	CreateSubagent(ctx context.Context, parent *uuid.UUID, spec domain.SubagentSpec, title string) (domain.Conversation, error)
	Get(ctx context.Context, id uuid.UUID) (domain.Conversation, error)
	History(ctx context.Context, id uuid.UUID) ([]*schema.Message, int64, error)
	AppendTurn(ctx context.Context, id uuid.UUID, lastID int64, msgs []*schema.Message, agentVersion int32, title string) error
}

// subagentTool returns the subagent tool for an agent whose config turns it
// on, if its depth allows another level.
func (b *Builder) subagentTool(cfg domain.ToolConfig, depth int) (tool.BaseTool, bool) {
	if _, on := cfg.State(SubagentToolName); !on || b.Subagents == nil {
		return nil, false
	}
	raw, err := SubagentSpec().DecodeSettings(cfg[SubagentToolName])
	if err != nil {
		slog.Warn("subagent settings are invalid; leaving the tool out", "err", err)
		return nil, false
	}
	settings := raw.(*SubagentSettings)
	if depth >= settings.depth() {
		return nil, false
	}
	t, err := kit.New(SubagentToolName, subagentDescription,
		func(ctx context.Context, in subagentIn) (subagentOut, error) {
			return b.runSubagent(ctx, in, cfg, settings, depth)
		})
	if err != nil {
		slog.Error("building the subagent tool", "err", err)
		return nil, false
	}
	return t, true
}

const subagentDescription = `Delegate a task to a subagent, which works on it with its own tools and context and returns its final answer.

- Give either agent (a stored agent's name, see list_agents if you have it) or model (an OpenRouter slug such as "deepseek/deepseek-v4.1-flash") for an ad-hoc worker. You can give both to run a stored agent on a different model.
- A new subagent starts with no context: put everything it needs in prompt.
- An ad-hoc worker gets your tools by default; pass tools to give it fewer.
- The result includes a conversation_id; pass it back with a new prompt to continue the same subagent.
- Use subagents for self-contained work (research, a side task, a second opinion from another model), not for trivial steps.`

type subagentIn struct {
	Description    string   `json:"description" jsonschema_description:"A short (3-5 word) label for the task."`
	Prompt         string   `json:"prompt" jsonschema_description:"The task, with all the context the subagent needs."`
	Agent          string   `json:"agent,omitempty" jsonschema_description:"A stored agent to run. Omit for an ad-hoc worker."`
	Model          string   `json:"model,omitempty" jsonschema_description:"OpenRouter model slug, e.g. deepseek/deepseek-v4.1-flash. Required for an ad-hoc worker; overrides a stored agent's model."`
	Tools          []string `json:"tools,omitempty" jsonschema_description:"For an ad-hoc worker: which of your tools it gets. Default: all of them."`
	ConversationID string   `json:"conversation_id,omitempty" jsonschema_description:"Continue an earlier subagent instead of starting a new one."`
}

type subagentOut struct {
	ConversationID   uuid.UUID `json:"conversation_id"`
	Output           string    `json:"output"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
}

func (b *Builder) runSubagent(ctx context.Context, in subagentIn, callerTools domain.ToolConfig, settings *SubagentSettings, depth int) (subagentOut, error) {
	var conv domain.Conversation
	var history []*schema.Message
	var lastID int64

	if in.ConversationID != "" {
		id, err := uuid.Parse(in.ConversationID)
		if err != nil {
			return subagentOut{}, fmt.Errorf("conversation_id: %w", err)
		}
		if conv, err = b.Subagents.Get(ctx, id); err != nil {
			return subagentOut{}, err
		}
		if conv.Subagent == nil {
			return subagentOut{}, fmt.Errorf("conversation %s is not a subagent's", id)
		}
		if history, lastID, err = b.Subagents.History(ctx, id); err != nil {
			return subagentOut{}, err
		}
	} else {
		spec, err := b.subagentSpec(ctx, in, callerTools, settings)
		if err != nil {
			return subagentOut{}, err
		}
		var parent *uuid.UUID
		if id, ok := domain.ConversationFrom(ctx); ok {
			parent = &id
		}
		if conv, err = b.Subagents.CreateSubagent(ctx, parent, spec, in.Description); err != nil {
			return subagentOut{}, err
		}
	}

	r, err := b.BuildSpec(ctx, *conv.Subagent, depth+1)
	if err != nil {
		return subagentOut{}, err
	}
	user := schema.UserMessage(in.Prompt)
	turn, runErr := r.Run(domain.WithConversation(ctx, conv.ID), Window(append(history, user), DefaultHistoryChars), Observer{})

	// Save what happened, as chat does: the whole turn, or on failure the
	// settled part if tools ran.
	produced := turn.Messages
	if runErr != nil {
		produced = Settled(produced)
	}
	if runErr == nil || ranTools(produced) {
		msgs := append([]*schema.Message{user}, produced...)
		if err := b.Subagents.AppendTurn(context.WithoutCancel(ctx), conv.ID, lastID, msgs, r.Info().Version, ""); err != nil {
			slog.Warn("saving subagent turn failed", "conversation", conv.ID, "err", err)
		}
	}
	if runErr != nil {
		return subagentOut{}, fmt.Errorf("subagent failed (conversation %s): %w", conv.ID, runErr)
	}
	return subagentOut{
		ConversationID: conv.ID, Output: turn.Final,
		PromptTokens: turn.PromptTokens, CompletionTokens: turn.CompletionTokens,
	}, nil
}

// subagentSpec checks a new subagent request against the caller's settings
// and tools.
func (b *Builder) subagentSpec(ctx context.Context, in subagentIn, callerTools domain.ToolConfig, settings *SubagentSettings) (domain.SubagentSpec, error) {
	spec := domain.SubagentSpec{Agent: in.Agent, Model: in.Model}
	if spec.Agent == "" && spec.Model == "" {
		return spec, errors.New("give agent, model, or both")
	}
	if spec.Model != "" {
		if len(settings.Models) > 0 && !slices.Contains(settings.Models, spec.Model) {
			return spec, fmt.Errorf("model %q isn't allowed; allowed models: %v", spec.Model, settings.Models)
		}
		if b.Catalog != nil {
			if err := b.Catalog.Check(ctx, spec.Model); err != nil {
				return spec, err
			}
		}
	}
	if spec.Agent != "" {
		if len(in.Tools) > 0 {
			return spec, errors.New("tools only applies to ad-hoc workers; a stored agent uses its own")
		}
		return spec, nil
	}

	// An ad-hoc worker gets the caller's tools, or a subset of them, with the
	// caller's settings.
	names := in.Tools
	if len(names) == 0 {
		names = callerTools.Enabled()
	}
	spec.Tools = domain.ToolConfig{}
	for _, n := range names {
		if _, on := callerTools.State(n); !on {
			return spec, fmt.Errorf("tool %q: a subagent can only have tools you have", n)
		}
		spec.Tools[n] = callerTools[n]
	}
	return spec, nil
}
