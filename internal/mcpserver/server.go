// Package mcpserver exposes orchestration services over MCP.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

const instructions = `Postgres-backed orchestration for agentic tasks. Manage immutable agent and workflow versions, start background workflow runs and inspect their durable results. Workflow graphs contain agent nodes and directed edges; every referenced agent must exist. Starting a run returns its current detail, including a run id for polling with get_run; an optional wait may return the final result immediately. Add and update predefined knowledge nodes with upsert_knowledge_node, read them with get_knowledge_node or list_knowledge_nodes, and find them by meaning with search_knowledge_nodes. Connect claims to supporting sources with add_supported_by, inspect those relationships with list_supported_by, and remove them with delete_supported_by.`

type Agents interface {
	List(ctx context.Context) ([]domain.Agent, error)
	Get(ctx context.Context, name string) (domain.AgentDetail, error)
	Versions(ctx context.Context, name string) ([]domain.AgentVersion, error)
	Create(ctx context.Context, name, description string, v domain.AgentVersionInput) (domain.AgentVersion, error)
	AddVersion(ctx context.Context, name string, v domain.AgentVersionInput, activate bool) (domain.AgentVersion, error)
	SetVersion(ctx context.Context, name string, version int32) error
}

type Workflows interface {
	List(ctx context.Context) ([]domain.Workflow, error)
	Search(ctx context.Context, query string, limit int32) ([]domain.WorkflowMatch, error)
	Get(ctx context.Context, name string) (domain.WorkflowDetail, error)
	Version(ctx context.Context, id uuid.UUID, version int32) (domain.WorkflowVersion, error)
	Versions(ctx context.Context, name string) ([]domain.WorkflowVersion, error)
	Create(ctx context.Context, name, description string, g workflow.Graph, notes string) (domain.WorkflowVersion, error)
	AddVersion(ctx context.Context, name string, g workflow.Graph, notes string, activate bool) (domain.WorkflowVersion, error)
	SetVersion(ctx context.Context, name string, version int32) error
	UpdateDescription(ctx context.Context, name, description string) error
	Delete(ctx context.Context, name string) error
}

type Runs interface {
	Start(ctx context.Context, workflow string, input json.RawMessage, triggeredBy string) (uuid.UUID, error)
	Get(ctx context.Context, id uuid.UUID) (domain.RunDetail, error)
	List(ctx context.Context, workflow string, limit int32) ([]domain.Run, error)
}

// Knowledge is implemented by *app.Knowledge.
type Knowledge interface {
	Get(ctx context.Context, kind string, id uuid.UUID) (domain.KnowledgeNode, error)
	List(ctx context.Context, kind string, limit int32) ([]domain.KnowledgeNode, error)
	Upsert(ctx context.Context, kind string, id uuid.UUID, data json.RawMessage, expectedRevision *int64) (domain.KnowledgeNode, error)
	Search(ctx context.Context, kind, query string, limit int32) ([]domain.KnowledgeNodeMatch, error)
	AddSupportedBy(ctx context.Context, claimID, sourceID uuid.UUID) (domain.KnowledgeEdge, error)
	DeleteSupportedBy(ctx context.Context, edgeID uuid.UUID) error
	ListSupportedBy(ctx context.Context, claimID, sourceID uuid.UUID) ([]domain.KnowledgeEdge, error)
}

type Tools interface {
	Assignable() []kit.Info
}

type Deps struct {
	Agents    Agents
	Workflows Workflows
	Runs      Runs
	Knowledge Knowledge
	Tools     Tools
}

func New(d Deps) (*mcp.Server, http.Handler) {
	s := mcp.NewServer(&mcp.Implementation{Name: "personal-agent", Version: "1.0.0"}, &mcp.ServerOptions{Instructions: instructions})
	registerAgentTools(s, d.Agents, d.Tools)
	registerWorkflowTools(s, d.Workflows)
	registerRunTools(s, d.Runs)
	registerKnowledgeTools(s, d.Knowledge)
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
	return s, new(http.CrossOriginProtection).Handler(h)
}

func add[In, Out any](s *mcp.Server, name, description string, annotations *mcp.ToolAnnotations, handler func(context.Context, In) (Out, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: description, Annotations: annotations}, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handler(ctx, in)
		return nil, out, err
	})
}

var (
	readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}
	additive = &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)}
	writing  = &mcp.ToolAnnotations{OpenWorldHint: boolPtr(false)}
	closedID = &mcp.ToolAnnotations{IdempotentHint: true, OpenWorldHint: boolPtr(false)}
)

func boolPtr(v bool) *bool { return &v }

func boundedLimit(v int32) int32 {
	if v <= 0 {
		return 10
	}
	return min(v, 100)
}

func rawObject(v map[string]any) json.RawMessage {
	if v == nil {
		return nil
	}
	raw, _ := json.Marshal(v)
	return raw
}

func object(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode MCP output: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode MCP output: %w", err)
	}
	return out, nil
}

func objects[T any](values []T) ([]map[string]any, error) {
	out := make([]map[string]any, len(values))
	for i, value := range values {
		var err error
		if out[i], err = object(value); err != nil {
			return nil, err
		}
	}
	return out, nil
}
