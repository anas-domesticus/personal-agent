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

const consumerInstructions = `Access the knowledge graph. Read, write, delete and semantically search nodes; read, write and delete edges; inspect immutable node and edge history; and discover registered node and edge schemas.`

const adminInstructions = `Postgres-backed orchestration for agentic tasks. Manage immutable agent and workflow versions, start background workflow runs and inspect their durable results. Workflow graphs contain agent nodes and directed edges; every referenced agent must exist. Starting a run returns its current detail, including a run id for polling with get_run; an optional wait may return the final result immediately. Discover knowledge graph schemas and create dynamic node and edge types.`

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

// Nodes provides generic knowledge-node operations.
type Nodes interface {
	GetNode(ctx context.Context, kind string, id uuid.UUID) (domain.KnowledgeNode, error)
	ListNodes(ctx context.Context, kind string, limit int32) ([]domain.KnowledgeNode, error)
	UpsertNode(ctx context.Context, kind string, id uuid.UUID, data, metadata json.RawMessage, expectedRevision *int64) (domain.KnowledgeNode, error)
	DeleteNode(ctx context.Context, kind string, id uuid.UUID, expectedRevision *int64) error
	SearchNodes(ctx context.Context, kind, query string, limit int32) ([]domain.KnowledgeNodeMatch, error)
	NodeHistory(ctx context.Context, kind string, id uuid.UUID, before int64, limit int) (domain.KnowledgeNodeHistory, error)
}

// Edges provides generic knowledge-edge operations.
type Edges interface {
	GetEdge(ctx context.Context, kind string, id uuid.UUID) (domain.KnowledgeEdge, error)
	ListEdges(ctx context.Context, kind string, sourceID, targetID uuid.UUID, limit int32) ([]domain.KnowledgeEdge, error)
	UpsertEdge(ctx context.Context, kind string, source, target domain.KnowledgeRef, data, metadata json.RawMessage, expectedRevision *int64) (domain.KnowledgeEdge, error)
	DeleteEdge(ctx context.Context, kind string, id uuid.UUID, expectedRevision *int64) error
	EdgeHistory(ctx context.Context, kind string, id uuid.UUID, before int64, limit int) (domain.KnowledgeEdgeHistory, error)
}

// Schema provides read-only knowledge-graph type discovery.
type Schema interface {
	GetNodeType(ctx context.Context, kind string) (domain.KnowledgeNodeType, error)
	ListNodeTypes(ctx context.Context) ([]domain.KnowledgeNodeType, error)
	GetEdgeType(ctx context.Context, kind string) (domain.KnowledgeEdgeType, error)
	ListEdgeTypes(ctx context.Context) ([]domain.KnowledgeEdgeType, error)
}

// SchemaAdmin creates dynamic knowledge-graph types.
type SchemaAdmin interface {
	CreateNodeType(ctx context.Context, kind string, dataSchema json.RawMessage) (domain.KnowledgeNodeType, error)
	CreateEdgeType(ctx context.Context, kind string, dataSchema json.RawMessage, endpoints []domain.KnowledgeEndpoint) (domain.KnowledgeEdgeType, error)
}

type Tools interface {
	Assignable() []kit.Info
}

type ConsumerDeps struct {
	Nodes  Nodes
	Edges  Edges
	Schema Schema
}

type AdminDeps struct {
	Agents      Agents
	Workflows   Workflows
	Runs        Runs
	Tools       Tools
	Schema      Schema
	SchemaAdmin SchemaAdmin
}

// NewConsumer exposes only data-plane graph tools and schema discovery.
func NewConsumer(d ConsumerDeps) (*mcp.Server, http.Handler) {
	s := mcp.NewServer(&mcp.Implementation{Name: "personal-agent-consumer", Version: "1.0.0"}, &mcp.ServerOptions{Instructions: consumerInstructions})
	registerNodeTools(s, d.Nodes)
	registerEdgeTools(s, d.Edges)
	registerSchemaTools(s, d.Schema)
	return s, handler(s)
}

// NewAdmin exposes orchestration tools, schema discovery and schema creation.
func NewAdmin(d AdminDeps) (*mcp.Server, http.Handler) {
	s := mcp.NewServer(&mcp.Implementation{Name: "personal-agent-admin", Version: "1.0.0"}, &mcp.ServerOptions{Instructions: adminInstructions})
	registerAgentTools(s, d.Agents, d.Tools)
	registerWorkflowTools(s, d.Workflows)
	registerRunTools(s, d.Runs)
	registerSchemaTools(s, d.Schema)
	registerSchemaAdminTools(s, d.SchemaAdmin)
	return s, handler(s)
}

func handler(s *mcp.Server) http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
	return new(http.CrossOriginProtection).Handler(h)
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
