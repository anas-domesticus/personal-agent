package knowledge_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/knowledge"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

func TestKindConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "objective", got: knowledge.ObjectiveKind, want: "objective"},
		{name: "workflow", got: knowledge.WorkflowKind, want: "workflow"},
		{name: "claim", got: knowledge.ClaimKind, want: "claim"},
		{name: "source", got: knowledge.SourceKind, want: "source"},
		{name: "agent", got: knowledge.AgentKind, want: "agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.got)
		})
	}
}

func TestDetailsValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      knowledge.Details
		wantErr string
	}{
		{name: "nonblank title", in: knowledge.Details{Title: "Research"}},
		{name: "blank title", in: knowledge.Details{Title: " \t\n"}, wantErr: "title is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.in.Validate()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestNodeText(t *testing.T) {
	t.Parallel()

	details := knowledge.Details{Title: "Research", Description: "Find supporting evidence."}
	tests := []struct {
		name string
		in   interface {
			NodeText() (string, string)
		}
	}{
		{name: "details", in: details},
		{name: "objective", in: knowledge.Objective{Details: details}},
		{name: "workflow", in: knowledge.Workflow{Details: details}},
		{name: "claim", in: knowledge.Claim{Details: details}},
		{name: "source", in: knowledge.Source{Details: details}},
		{name: "agent", in: knowledge.Agent{Details: details}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			title, description := tt.in.NodeText()
			require.Equal(t, details.Title, title)
			require.Equal(t, details.Description, description)
		})
	}
}

func TestObjectiveValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      knowledge.Objective
		wantErr string
	}{
		{name: "title", in: knowledge.Objective{Details: knowledge.Details{Title: "Improve research"}}},
		{name: "blank title", in: knowledge.Objective{Details: knowledge.Details{Title: " \t\n"}}, wantErr: "title is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.in.Validate()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestWorkflowValidate(t *testing.T) {
	t.Parallel()

	validGraph := workflow.Graph{Nodes: []workflow.Node{{ID: "research", Agent: "researcher"}}}
	tests := []struct {
		name    string
		in      knowledge.Workflow
		wantErr string
	}{
		{name: "valid workflow", in: knowledge.Workflow{Details: knowledge.Details{Title: "Research"}, Graph: validGraph}},
		{name: "blank title", in: knowledge.Workflow{Details: knowledge.Details{Title: " "}, Graph: validGraph}, wantErr: "title is required"},
		{name: "invalid graph", in: knowledge.Workflow{Details: knowledge.Details{Title: "Research"}}, wantErr: "graph has no nodes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.in.Validate()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestClaimValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      knowledge.Claim
		wantErr string
	}{
		{name: "nonblank title", in: knowledge.Claim{Details: knowledge.Details{Title: "The sky is blue."}}},
		{name: "blank title", in: knowledge.Claim{Details: knowledge.Details{Title: " \t\n"}}, wantErr: "title is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.in.Validate()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestSourceValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      knowledge.Source
		wantErr string
	}{
		{name: "title", in: knowledge.Source{Details: knowledge.Details{Title: "Documentation"}}},
		{name: "URI", in: knowledge.Source{Details: knowledge.Details{Title: "Documentation"}, URI: "https://example.com"}},
		{name: "object metadata", in: knowledge.Source{Details: knowledge.Details{Title: "Documentation"}, Metadata: json.RawMessage(`{"author":"Ada"}`)}},
		{name: "blank title with URI", in: knowledge.Source{Details: knowledge.Details{Title: " "}, URI: "https://example.com"}, wantErr: "title is required"},
		{name: "array metadata", in: knowledge.Source{Details: knowledge.Details{Title: "Documentation"}, Metadata: json.RawMessage(`[]`)}, wantErr: "metadata must be a JSON object"},
		{name: "null metadata", in: knowledge.Source{Details: knowledge.Details{Title: "Documentation"}, Metadata: json.RawMessage(`null`)}, wantErr: "metadata must be a JSON object"},
		{name: "malformed metadata", in: knowledge.Source{Details: knowledge.Details{Title: "Documentation"}, Metadata: json.RawMessage(`{"author":`)}, wantErr: "metadata must be a JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.in.Validate()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestAgentValidate(t *testing.T) {
	t.Parallel()

	valid := knowledge.Agent{
		Details:      knowledge.Details{Title: "Researcher"},
		SystemPrompt: "Research the question.",
		Model:        "openai/gpt-5",
	}
	tests := []struct {
		name    string
		change  func(*knowledge.Agent)
		wantErr string
	}{
		{name: "valid agent"},
		{name: "object output schema", change: func(a *knowledge.Agent) {
			a.OutputSchema = json.RawMessage(`{"type":"object"}`)
		}},
		{name: "blank title", change: func(a *knowledge.Agent) { a.Title = " " }, wantErr: "title is required"},
		{name: "blank system prompt", change: func(a *knowledge.Agent) {
			a.SystemPrompt = "\t"
		}, wantErr: "system prompt is required"},
		{name: "blank model", change: func(a *knowledge.Agent) { a.Model = "\n" }, wantErr: "model is required"},
		{name: "invalid config", change: func(a *knowledge.Agent) {
			a.Config.MaxIterations = -1
		}, wantErr: "max_iterations"},
		{name: "array output schema", change: func(a *knowledge.Agent) {
			a.OutputSchema = json.RawMessage(`[]`)
		}, wantErr: "output schema must be a JSON object"},
		{name: "null output schema", change: func(a *knowledge.Agent) {
			a.OutputSchema = json.RawMessage(`null`)
		}, wantErr: "output schema must be a JSON object"},
		{name: "malformed output schema", change: func(a *knowledge.Agent) {
			a.OutputSchema = json.RawMessage(`{"type":`)
		}, wantErr: "not valid JSON"},
		{name: "invalid output schema keyword", change: func(a *knowledge.Agent) {
			a.OutputSchema = json.RawMessage(`{"type":7}`)
		}, wantErr: "output schema must have"},
		{name: "invalid tool config", change: func(a *knowledge.Agent) {
			a.Config.Tools = domain.ToolConfig{"search": json.RawMessage(`[]`)}
		}, wantErr: "tools.search"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := valid
			if tt.change != nil {
				tt.change(&in)
			}
			err := in.Validate()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}
