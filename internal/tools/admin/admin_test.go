package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/admin"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

func TestNamesMatchTools(t *testing.T) {
	t.Parallel()
	specs, err := admin.New(admin.Deps{})
	require.NoError(t, err)
	got := make([]string, 0, len(specs))
	for _, s := range specs {
		got = append(got, s.Name)
		assert.True(t, s.Admin, "%s is not marked admin", s.Name)
	}
	assert.ElementsMatch(t, admin.Names, got)
}

// deps is a full set of mocked admin dependencies.
type deps struct {
	agents    *admin.MockAgents
	workflows *admin.MockWorkflows
	runs      *admin.MockRuns
	tools     *admin.MockTools
}

// build returns the admin tools by name, over fresh mocks.
func build(t *testing.T) (deps, map[string]tool.InvokableTool) {
	t.Helper()
	d := deps{
		agents:    admin.NewMockAgents(t),
		workflows: admin.NewMockWorkflows(t),
		runs:      admin.NewMockRuns(t),
		tools:     admin.NewMockTools(t),
	}
	specs, err := admin.New(admin.Deps{Agents: d.agents, Workflows: d.workflows, Runs: d.runs, Tools: d.tools})
	require.NoError(t, err)
	out := map[string]tool.InvokableTool{}
	for _, s := range specs {
		tl, err := s.New(nil)
		require.NoError(t, err)
		out[s.Name] = tl
	}
	return d, out
}

func ptr[T any](v T) *T { return &v }

func TestTools(t *testing.T) {
	t.Parallel()
	runID := uuid.MustParse("8f9a3c1e-0000-4000-8000-000000000001")
	graph := workflow.Graph{Nodes: []workflow.Node{{ID: "a", Agent: "helper"}}, Edges: []workflow.Edge{}}
	withTranscript := func() domain.RunDetail {
		return domain.RunDetail{
			Run:   domain.Run{ID: runID, Workflow: "wf", Status: domain.RunSucceeded},
			Steps: []domain.Step{{Node: "a", Status: domain.RunSucceeded, Transcript: json.RawMessage(`[{"role":"user"}]`)}},
		}
	}
	withoutTranscript := withTranscript()
	withoutTranscript.Steps[0].Transcript = nil

	tests := []struct {
		name    string
		tool    string
		args    string
		caller  string // the calling agent, if any
		setup   func(d deps)
		want    any // the result's data
		wantErr string
	}{
		{
			name: "list_tools", tool: "list_tools", args: `{}`,
			setup: func(d deps) {
				d.tools.EXPECT().Assignable().Return([]kit.Info{{Name: "web_search", Description: "Search."}}).Once()
			},
			want: []kit.Info{{Name: "web_search", Description: "Search."}},
		},
		{
			name: "list_agents", tool: "list_agents", args: `{}`,
			setup: func(d deps) {
				d.agents.EXPECT().List(mock.Anything).Return([]domain.Agent{{Name: "helper", CurrentVersion: ptr[int32](2)}}, nil).Once()
			},
			want: []domain.Agent{{Name: "helper", CurrentVersion: ptr[int32](2)}},
		},
		{
			name: "get_agent", tool: "get_agent", args: `{"name":"helper"}`,
			setup: func(d deps) {
				d.agents.EXPECT().Get(mock.Anything, "helper").Return(domain.AgentDetail{Agent: domain.Agent{Name: "helper"}}, nil).Once()
			},
			want: domain.AgentDetail{Agent: domain.Agent{Name: "helper"}},
		},
		{
			name: "get_agent error goes to the model", tool: "get_agent", args: `{"name":"nope"}`,
			setup: func(d deps) {
				d.agents.EXPECT().Get(mock.Anything, "nope").Return(domain.AgentDetail{}, domain.ErrNotFound).Once()
			},
			wantErr: domain.ErrNotFound.Error(),
		},
		{
			name: "list_agent_versions", tool: "list_agent_versions", args: `{"name":"helper"}`,
			setup: func(d deps) {
				d.agents.EXPECT().Versions(mock.Anything, "helper").Return([]domain.AgentVersion{{Version: 1}}, nil).Once()
			},
			want: []domain.AgentVersion{{Version: 1}},
		},
		{
			name: "create_agent passes config and schema as JSON", tool: "create_agent",
			args: `{"name":"helper","description":"Helps.","system_prompt":"Be useful.","model":"m/x",
				"config":{"tools":{"read":true}},"output_schema":{"type":"object"},"notes":"first"}`,
			setup: func(d deps) {
				d.agents.EXPECT().Create(mock.Anything, "helper", "Helps.", domain.AgentVersionInput{
					SystemPrompt: "Be useful.", Model: "m/x",
					Config:       json.RawMessage(`{"tools":{"read":true}}`),
					OutputSchema: json.RawMessage(`{"type":"object"}`),
					Notes:        "first",
				}).Return(domain.AgentVersion{Version: 1}, nil).Once()
			},
			want: domain.AgentVersion{Version: 1},
		},
		{
			name: "add_agent_version without config", tool: "add_agent_version",
			args: `{"name":"helper","system_prompt":"p","model":"m/x","activate":true}`,
			setup: func(d deps) {
				d.agents.EXPECT().AddVersion(mock.Anything, "helper", domain.AgentVersionInput{SystemPrompt: "p", Model: "m/x"}, true).
					Return(domain.AgentVersion{Version: 2}, nil).Once()
			},
			want: domain.AgentVersion{Version: 2},
		},
		{
			name: "set_agent_version", tool: "set_agent_version", args: `{"name":"helper","version":1}`,
			setup: func(d deps) { d.agents.EXPECT().SetVersion(mock.Anything, "helper", int32(1)).Return(nil).Once() },
			want:  struct{}{},
		},
		{
			name: "list_workflows", tool: "list_workflows", args: `{}`,
			setup: func(d deps) {
				d.workflows.EXPECT().List(mock.Anything).Return([]domain.Workflow{{Name: "wf"}}, nil).Once()
			},
			want: []domain.Workflow{{Name: "wf"}},
		},
		{
			name: "search_workflows uses the default limit", tool: "search_workflows", args: `{"query":"email"}`,
			setup: func(d deps) {
				d.workflows.EXPECT().Search(mock.Anything, "email", int32(10)).Return([]domain.WorkflowMatch{}, nil).Once()
			},
			want: []domain.WorkflowMatch{},
		},
		{
			name: "search_workflows clamps the limit", tool: "search_workflows", args: `{"query":"email","limit":500}`,
			setup: func(d deps) {
				d.workflows.EXPECT().Search(mock.Anything, "email", int32(50)).Return([]domain.WorkflowMatch{}, nil).Once()
			},
			want: []domain.WorkflowMatch{},
		},
		{
			name: "get_workflow", tool: "get_workflow", args: `{"name":"wf"}`,
			setup: func(d deps) {
				d.workflows.EXPECT().Get(mock.Anything, "wf").Return(domain.WorkflowDetail{Workflow: domain.Workflow{Name: "wf"}}, nil).Once()
			},
			want: domain.WorkflowDetail{Workflow: domain.Workflow{Name: "wf"}},
		},
		{
			name: "list_workflow_versions", tool: "list_workflow_versions", args: `{"name":"wf"}`,
			setup: func(d deps) {
				d.workflows.EXPECT().Versions(mock.Anything, "wf").Return([]domain.WorkflowVersion{{Version: 3}}, nil).Once()
			},
			want: []domain.WorkflowVersion{{Version: 3}},
		},
		{
			name: "create_workflow", tool: "create_workflow",
			args: `{"name":"wf","description":"Does it.","graph":{"nodes":[{"id":"a","agent":"helper"}],"edges":[]},"notes":"n"}`,
			setup: func(d deps) {
				d.workflows.EXPECT().Create(mock.Anything, "wf", "Does it.", graph, "n").Return(domain.WorkflowVersion{Version: 1}, nil).Once()
			},
			want: domain.WorkflowVersion{Version: 1},
		},
		{
			name: "add_workflow_version", tool: "add_workflow_version",
			args: `{"name":"wf","graph":{"nodes":[{"id":"a","agent":"helper"}],"edges":[]},"notes":"n","activate":false}`,
			setup: func(d deps) {
				d.workflows.EXPECT().AddVersion(mock.Anything, "wf", graph, "n", false).Return(domain.WorkflowVersion{Version: 2}, nil).Once()
			},
			want: domain.WorkflowVersion{Version: 2},
		},
		{
			name: "set_workflow_version", tool: "set_workflow_version", args: `{"name":"wf","version":1}`,
			setup: func(d deps) { d.workflows.EXPECT().SetVersion(mock.Anything, "wf", int32(1)).Return(nil).Once() },
			want:  struct{}{},
		},
		{
			name: "update_workflow_description", tool: "update_workflow_description", args: `{"name":"wf","description":"New."}`,
			setup: func(d deps) { d.workflows.EXPECT().UpdateDescription(mock.Anything, "wf", "New.").Return(nil).Once() },
			want:  struct{}{},
		},
		{
			name: "update_workflow_description error", tool: "update_workflow_description", args: `{"name":"wf","description":"New."}`,
			setup: func(d deps) {
				d.workflows.EXPECT().UpdateDescription(mock.Anything, "wf", "New.").Return(errors.New("db down")).Once()
			},
			wantErr: "db down",
		},
		{
			name: "run_workflow records the caller and strips transcripts", tool: "run_workflow",
			args: `{"name":"wf","input":{"x":1}}`, caller: "planner",
			setup: func(d deps) {
				d.runs.EXPECT().Start(mock.Anything, "wf", json.RawMessage(`{"x":1}`), "agent:planner").Return(runID, nil).Once()
				d.runs.EXPECT().Get(mock.Anything, runID).Return(withTranscript(), nil).Once()
			},
			want: withoutTranscript,
		},
		{
			name: "run_workflow without a caller", tool: "run_workflow", args: `{"name":"wf"}`,
			setup: func(d deps) {
				d.runs.EXPECT().Start(mock.Anything, "wf", json.RawMessage(nil), "agent").Return(runID, nil).Once()
				d.runs.EXPECT().Get(mock.Anything, runID).Return(withTranscript(), nil).Once()
			},
			want: withoutTranscript,
		},
		{
			name: "run_workflow waits until the run finishes", tool: "run_workflow", args: `{"name":"wf","wait_seconds":30}`,
			setup: func(d deps) {
				d.runs.EXPECT().Start(mock.Anything, "wf", json.RawMessage(nil), "agent").Return(runID, nil).Once()
				// Once to wait (already finished), once to report.
				d.runs.EXPECT().Get(mock.Anything, runID).RunAndReturn(func(context.Context, uuid.UUID) (domain.RunDetail, error) {
					return withTranscript(), nil
				}).Times(2)
			},
			want: withoutTranscript,
		},
		{
			name: "run_workflow start error", tool: "run_workflow", args: `{"name":"wf"}`,
			setup: func(d deps) {
				d.runs.EXPECT().Start(mock.Anything, "wf", json.RawMessage(nil), "agent").Return(uuid.Nil, domain.ErrBusy).Once()
			},
			wantErr: domain.ErrBusy.Error(),
		},
		{
			name: "get_run strips transcripts by default", tool: "get_run", args: `{"id":"` + runID.String() + `"}`,
			setup: func(d deps) { d.runs.EXPECT().Get(mock.Anything, runID).Return(withTranscript(), nil).Once() },
			want:  withoutTranscript,
		},
		{
			name: "get_run with transcripts", tool: "get_run", args: `{"id":"` + runID.String() + `","include_transcripts":true}`,
			setup: func(d deps) { d.runs.EXPECT().Get(mock.Anything, runID).Return(withTranscript(), nil).Once() },
			want:  withTranscript(),
		},
		{
			name: "list_runs", tool: "list_runs", args: `{"workflow":"wf","limit":3}`,
			setup: func(d deps) {
				d.runs.EXPECT().List(mock.Anything, "wf", int32(3)).Return([]domain.Run{{ID: runID}}, nil).Once()
			},
			want: []domain.Run{{ID: runID}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d, tools := build(t)
			tt.setup(d)
			ctx := context.Background()
			if tt.caller != "" {
				ctx = domain.WithCaller(ctx, tt.caller)
			}
			require.Contains(t, tools, tt.tool)

			out, err := tools[tt.tool].InvokableRun(ctx, tt.args)
			require.NoError(t, err)
			if tt.wantErr != "" {
				assert.JSONEq(t, mustJSON(t, map[string]any{"ok": false, "error": tt.wantErr}), out)
				return
			}
			assert.JSONEq(t, mustJSON(t, map[string]any{"ok": true, "data": tt.want}), out)
		})
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
