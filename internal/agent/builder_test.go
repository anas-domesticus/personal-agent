package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/llm"
	"github.com/anas-domesticus/personal-agent/internal/mocks"
)

// These unit tests build agents from mocked collaborators; running them is
// covered by the integration tests on testenv.

func TestBuildSpec(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	temp := float32(0.3)
	maxTokens := 512
	stored := func() domain.AgentDetail {
		return domain.AgentDetail{
			Agent: domain.Agent{ID: id, Name: "triager", Description: "ranks"},
			Current: &domain.AgentVersion{
				Version: 3, SystemPrompt: "Rank.", Model: "openai/gpt-5-mini",
				Config:       domain.AgentConfig{Temperature: &temp, MaxTokens: &maxTokens, Tools: domain.On("current_time")},
				OutputSchema: json.RawMessage(`{"type":"object"}`),
			},
		}
	}

	tests := []struct {
		name      string
		spec      domain.SubagentSpec
		depth     int
		setup     func(src *agent.MockSource, ts *agent.MockToolset)
		wantInfo  agent.Info
		wantModel llm.Spec
		wantErr   string
	}{
		{
			name:    "ad-hoc worker needs a model",
			spec:    domain.SubagentSpec{},
			wantErr: "needs a model",
		},
		{
			name: "unknown agent",
			spec: domain.SubagentSpec{Agent: "ghost"},
			setup: func(src *agent.MockSource, _ *agent.MockToolset) {
				src.EXPECT().Get(mock.Anything, "ghost").Return(domain.AgentDetail{}, domain.ErrNotFound).Once()
			},
			wantErr: "not found",
		},
		{
			name: "agent without a current version",
			spec: domain.SubagentSpec{Agent: "draft"},
			setup: func(src *agent.MockSource, _ *agent.MockToolset) {
				src.EXPECT().Get(mock.Anything, "draft").Return(domain.AgentDetail{Agent: domain.Agent{Name: "draft"}}, nil).Once()
			},
			wantErr: "has no current version",
		},
		{
			name: "stored agent",
			spec: domain.SubagentSpec{Agent: "triager"},
			setup: func(src *agent.MockSource, ts *agent.MockToolset) {
				src.EXPECT().Get(mock.Anything, "triager").Return(stored(), nil).Once()
				ts.EXPECT().For(domain.On("current_time")).Return(nil).Once()
			},
			wantInfo: agent.Info{Name: "triager", AgentID: id, Version: 3, Model: "openai/gpt-5-mini",
				OutputSchema: json.RawMessage(`{"type":"object"}`)},
			wantModel: llm.Spec{Model: "openai/gpt-5-mini", Temperature: &temp, MaxTokens: &maxTokens},
		},
		{
			name: "stored agent on another model",
			spec: domain.SubagentSpec{Agent: "triager", Model: "deepseek/deepseek-v4.1-flash"},
			setup: func(src *agent.MockSource, ts *agent.MockToolset) {
				src.EXPECT().Get(mock.Anything, "triager").Return(stored(), nil).Once()
				ts.EXPECT().For(domain.On("current_time")).Return(nil).Once()
			},
			wantInfo: agent.Info{Name: "triager", AgentID: id, Version: 3, Model: "deepseek/deepseek-v4.1-flash",
				OutputSchema: json.RawMessage(`{"type":"object"}`)},
			wantModel: llm.Spec{Model: "deepseek/deepseek-v4.1-flash", Temperature: &temp, MaxTokens: &maxTokens},
		},
		{
			name:  "ad-hoc worker",
			spec:  domain.SubagentSpec{Model: "fake/worker", Tools: domain.On("current_time")},
			depth: 1,
			setup: func(_ *agent.MockSource, ts *agent.MockToolset) {
				ts.EXPECT().For(domain.On("current_time")).Return(nil).Once()
			},
			wantInfo:  agent.Info{Name: "subagent", Model: "fake/worker"},
			wantModel: llm.Spec{Model: "fake/worker"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := agent.NewMockSource(t)
			ts := agent.NewMockToolset(t)
			if tt.setup != nil {
				tt.setup(src, ts)
			}
			var gotModel llm.Spec
			b := &agent.Builder{
				Agents: src,
				Tools:  ts,
				Models: func(_ context.Context, spec llm.Spec) (model.ToolCallingChatModel, error) {
					gotModel = spec
					return mocks.NewMockToolCallingChatModel(t), nil
				},
			}

			r, err := b.BuildSpec(context.Background(), tt.spec, tt.depth)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantInfo, r.Info())
			assert.Equal(t, tt.wantModel, gotModel)
		})
	}
}

func TestBuildSpecModelErrors(t *testing.T) {
	t.Parallel()

	modelErr := errors.New("model unavailable")
	tests := []struct {
		name     string
		modelErr error
		nilModel bool
		wantErr  string
	}{
		{name: "model factory failure", modelErr: modelErr, wantErr: "model unavailable"},
		{name: "Eino rejects a missing model", nilModel: true, wantErr: `build agent "subagent": agent 'Model' is required`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := agent.NewMockToolset(t)
			ts.EXPECT().For(domain.ToolConfig(nil)).Return(nil).Once()
			b := &agent.Builder{
				Agents: agent.NewMockSource(t),
				Tools:  ts,
				Models: func(context.Context, llm.Spec) (model.ToolCallingChatModel, error) {
					if tt.modelErr != nil {
						return nil, tt.modelErr
					}
					if tt.nilModel {
						return nil, nil
					}
					return mocks.NewMockToolCallingChatModel(t), nil
				},
			}

			r, err := b.BuildSpec(context.Background(), domain.SubagentSpec{Model: "fake/worker"}, 0)

			assert.Nil(t, r)
			require.ErrorContains(t, err, tt.wantErr)
			if tt.modelErr != nil {
				assert.ErrorIs(t, err, tt.modelErr)
			}
		})
	}
}

func TestBuildSpecReportsToolNames(t *testing.T) {
	t.Parallel()

	tl := mocks.NewMockInvokableTool(t)
	tl.EXPECT().Info(mock.Anything).Return(&schema.ToolInfo{Name: "current_time"}, nil).Once()
	ts := agent.NewMockToolset(t)
	ts.EXPECT().For(domain.On("current_time")).Return([]tool.BaseTool{tl}).Once()
	b := &agent.Builder{
		Agents: agent.NewMockSource(t),
		Tools:  ts,
		Models: func(context.Context, llm.Spec) (model.ToolCallingChatModel, error) {
			return mocks.NewMockToolCallingChatModel(t), nil
		},
	}

	r, err := b.BuildSpec(context.Background(), domain.SubagentSpec{
		Model: "fake/worker",
		Tools: domain.On("current_time"),
	}, 0)

	require.NoError(t, err)
	assert.Equal(t, []string{"current_time"}, r.Info().Tools)
}
