// Package domain holds the types every layer shares: agents, workflows, runs
// and conversations, and the errors that describe them.
//
// The catalog, runs and chat services return these types rather than rows
// generated from SQL, so tools and UIs don't depend on query shapes.
package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

var (
	// ErrNotFound means a named agent, workflow, version, run or
	// conversation doesn't exist.
	ErrNotFound = errors.New("not found")
	// ErrExists means a name is already taken.
	ErrExists = errors.New("already exists")
	// ErrBusy means a conversation is already handling a turn.
	ErrBusy = errors.New("busy")
	// ErrConflict means data changed underneath an operation.
	ErrConflict = errors.New("conflict")
	// ErrUnavailable means a service is shutting down and takes no new work.
	ErrUnavailable = errors.New("unavailable")
)

// --- Agents -------------------------------------------------------------------

type Agent struct {
	ID             uuid.UUID `json:"-"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	CurrentVersion *int32    `json:"current_version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// AgentVersion is one immutable configuration of an agent.
type AgentVersion struct {
	AgentID      uuid.UUID       `json:"-"`
	Version      int32           `json:"version"`
	SystemPrompt string          `json:"system_prompt"`
	Model        string          `json:"model"`
	Config       AgentConfig     `json:"config"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	Notes        string          `json:"notes"`
	CreatedAt    time.Time       `json:"created_at"`
}

// AgentVersionInput is a new agent version.
type AgentVersionInput struct {
	SystemPrompt string
	Model        string
	Config       json.RawMessage // optional object; see AgentConfig
	OutputSchema json.RawMessage // optional JSON Schema object
	Notes        string
}

// AgentDetail is an agent with its current version, if it has one.
type AgentDetail struct {
	Agent
	Current *AgentVersion `json:"current,omitempty"`
}

// --- Workflows ----------------------------------------------------------------

type Workflow struct {
	ID             uuid.UUID `json:"-"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	CurrentVersion *int32    `json:"current_version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// WorkflowVersion is one immutable graph of a workflow.
type WorkflowVersion struct {
	WorkflowID uuid.UUID      `json:"-"`
	Version    int32          `json:"version"`
	Graph      workflow.Graph `json:"graph"`
	Notes      string         `json:"notes"`
	CreatedAt  time.Time      `json:"created_at"`
}

type WorkflowDetail struct {
	Workflow
	Current *WorkflowVersion `json:"current,omitempty"`
}

// WorkflowMatch is a search result.
type WorkflowMatch struct {
	Workflow
	Score float64 `json:"score"`
}

// --- Knowledge graph ----------------------------------------------------------

// KnowledgeNode is one current predefined graph node.
type KnowledgeNode struct {
	ID          uuid.UUID       `json:"id"`
	Kind        string          `json:"kind"`
	Revision    int64           `json:"revision"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Data        json.RawMessage `json:"data"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// KnowledgeNodeMatch is a semantic graph search result.
type KnowledgeNodeMatch struct {
	KnowledgeNode
	Score float64 `json:"score"`
}

// KnowledgeEdge is one predefined graph relationship.
type KnowledgeEdge struct {
	ID         uuid.UUID       `json:"id"`
	Kind       string          `json:"kind"`
	SourceID   uuid.UUID       `json:"source_id"`
	SourceKind string          `json:"source_kind"`
	TargetID   uuid.UUID       `json:"target_id"`
	TargetKind string          `json:"target_kind"`
	Data       json.RawMessage `json:"data"`
	Revision   int64           `json:"revision"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// --- Runs ---------------------------------------------------------------------

type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunRunning   RunStatus = "running"
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
)

// Terminal reports whether a run with this status has finished.
func (s RunStatus) Terminal() bool { return s == RunSucceeded || s == RunFailed }

// Run is one execution of a workflow version.
type Run struct {
	ID              uuid.UUID       `json:"id"`
	WorkflowID      uuid.UUID       `json:"-"`
	Workflow        string          `json:"workflow"`
	WorkflowVersion int32           `json:"workflow_version"`
	Status          RunStatus       `json:"status"`
	Input           json.RawMessage `json:"input,omitempty"`
	Output          json.RawMessage `json:"output,omitempty"`
	Error           string          `json:"error,omitempty"`
	TriggeredBy     string          `json:"triggered_by"`
	CreatedAt       time.Time       `json:"created_at"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
}

// Step is one node of a run.
type Step struct {
	ID               int64           `json:"-"`
	Node             string          `json:"node"`
	Agent            string          `json:"agent,omitempty"`
	AgentVersion     *int32          `json:"agent_version,omitempty"`
	Status           RunStatus       `json:"status"`
	Input            json.RawMessage `json:"input"`
	Output           json.RawMessage `json:"output,omitempty"`
	Error            string          `json:"error,omitempty"`
	Transcript       json.RawMessage `json:"transcript,omitempty"`
	PromptTokens     int32           `json:"prompt_tokens"`
	CompletionTokens int32           `json:"completion_tokens"`
	StartedAt        time.Time       `json:"started_at"`
	FinishedAt       *time.Time      `json:"finished_at,omitempty"`
}

type RunDetail struct {
	Run
	Steps []Step `json:"steps"`
}

// --- Conversations ------------------------------------------------------------

// Conversation is a chat with an agent. A subagent's conversation has a
// Subagent spec and, when it was started from another conversation, a
// ParentID.
type Conversation struct {
	ID        uuid.UUID     `json:"id"`
	AgentID   *uuid.UUID    `json:"-"`
	Agent     string        `json:"agent,omitempty"`
	ParentID  *uuid.UUID    `json:"parent_id,omitempty"`
	Subagent  *SubagentSpec `json:"subagent,omitempty"`
	Title     string        `json:"title"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// SubagentSpec is what a subagent was built from: a stored agent, optionally
// on a different model, or an ad-hoc worker with a model and tools.
type SubagentSpec struct {
	Agent string     `json:"agent,omitempty"`
	Model string     `json:"model,omitempty"`
	Tools ToolConfig `json:"tools,omitempty"`
}

type conversationKey struct{}

// WithConversation records the conversation an agent is running in, so a
// subagent's conversation can be linked to it.
func WithConversation(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, conversationKey{}, id)
}

// ConversationFrom returns the current conversation, if any.
func ConversationFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(conversationKey{}).(uuid.UUID)
	return id, ok
}

// --- Calling agent ------------------------------------------------------------

type callerKey struct{}

// WithCaller records which agent is acting, so tools can attribute what they
// do (e.g. an agent's name, or who triggered a run).
func WithCaller(ctx context.Context, agent string) context.Context {
	return context.WithValue(ctx, callerKey{}, agent)
}

// Caller returns the acting agent's name, or "".
func Caller(ctx context.Context) string {
	name, _ := ctx.Value(callerKey{}).(string)
	return name
}
