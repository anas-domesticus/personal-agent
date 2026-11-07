package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
)

func TestWorkflowVersionAndDelete(t *testing.T) {
	t.Parallel()
	workflows := NewMockWorkflows(t)
	srv, _ := New(Deps{
		Agents: NewMockAgents(t), Workflows: workflows, Runs: NewMockRuns(t), Tools: NewMockTools(t),
	})
	session := connect(t, srv)
	id := uuid.New()
	current := int32(3)
	workflows.EXPECT().Get(mock.Anything, "daily").Return(domain.WorkflowDetail{
		Workflow: domain.Workflow{ID: id, Name: "daily", CurrentVersion: &current},
		Current:  &domain.WorkflowVersion{Version: 3},
	}, nil).Once()
	workflows.EXPECT().Version(mock.Anything, id, int32(2)).Return(domain.WorkflowVersion{Version: 2}, nil).Once()
	got, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "get_workflow", Arguments: map[string]any{"name": "daily", "version": 2},
	})
	require.NoError(t, err)
	require.False(t, got.IsError)
	content := structured(t, got)
	assert.InEpsilon(t, 3, content["workflow"].(map[string]any)["current_version"], 0)
	assert.InEpsilon(t, 2, content["version"].(map[string]any)["version"], 0)

	workflows.EXPECT().Delete(mock.Anything, "daily").Return(domain.ErrConflict).Once()
	deleted, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "delete_workflow", Arguments: map[string]any{"name": "daily"},
	})
	require.NoError(t, err)
	assert.True(t, deleted.IsError)
}

func TestRunWaitAndTranscripts(t *testing.T) {
	t.Parallel()
	runs := NewMockRuns(t)
	srv, _ := New(Deps{
		Agents: NewMockAgents(t), Workflows: NewMockWorkflows(t), Runs: runs, Tools: NewMockTools(t),
	})
	session := connect(t, srv)
	id := uuid.New()
	runs.EXPECT().Start(mock.Anything, "daily", json.RawMessage(nil), "mcp").Return(id, nil).Once()
	runs.EXPECT().Get(mock.Anything, id).Return(domain.RunDetail{Run: domain.Run{ID: id, Status: domain.RunRunning}}, nil).Once()
	runs.EXPECT().Get(mock.Anything, id).Return(domain.RunDetail{
		Run:   domain.Run{ID: id, Status: domain.RunSucceeded},
		Steps: []domain.Step{{Node: "only", Transcript: json.RawMessage(`[{"role":"assistant"}]`)}},
	}, nil).Twice()
	started, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "run_workflow", Arguments: map[string]any{"name": "daily", "wait_seconds": 2},
	})
	require.NoError(t, err)
	require.False(t, started.IsError)
	content := structured(t, started)
	assert.Equal(t, "succeeded", content["status"])
	assert.NotContains(t, content["steps"].([]any)[0], "transcript")

	runs.EXPECT().Get(mock.Anything, id).Return(domain.RunDetail{
		Run:   domain.Run{ID: id, Status: domain.RunSucceeded},
		Steps: []domain.Step{{Node: "only", Transcript: json.RawMessage(`[{"role":"assistant"}]`)}},
	}, nil).Once()
	withTranscripts, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "get_run", Arguments: map[string]any{"id": id.String(), "include_transcripts": true},
	})
	require.NoError(t, err)
	require.False(t, withTranscripts.IsError)
	content = structured(t, withTranscripts)
	assert.Contains(t, content["steps"].([]any)[0], "transcript")
}
