package agent_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/components/model"
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
	stored := func(admin bool) domain.AgentDetail {
		return domain.AgentDetail{
			Agent: domain.Agent{ID: id, Name: "triager", Description: "ranks", Admin: admin},
			Current: &domain.AgentVersion{
				Version: 3, SystemPrompt: "Rank.", Model: "openai/gpt-5-mini",
				Config:       domain.AgentConfig{Temperature: &temp, Tools: domain.On("current_time")},
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
				src.EXPECT().Get(mock.Anything, "triager").Return(stored(false), nil).Once()
				ts.EXPECT().For(domain.On("current_time"), false).Return(nil).Once()
			},
			wantInfo: agent.Info{Name: "triager", AgentID: id, Version: 3, Model: "openai/gpt-5-mini",
				OutputSchema: json.RawMessage(`{"type":"object"}`)},
			wantModel: llm.Spec{Model: "openai/gpt-5-mini", Temperature: &temp},
		},
		{
			name: "stored agent on another model",
			spec: domain.SubagentSpec{Agent: "triager", Model: "deepseek/deepseek-v4.1-flash"},
			setup: func(src *agent.MockSource, ts *agent.MockToolset) {
				src.EXPECT().Get(mock.Anything, "triager").Return(stored(false), nil).Once()
				ts.EXPECT().For(domain.On("current_time"), false).Return(nil).Once()
			},
			wantInfo: agent.Info{Name: "triager", AgentID: id, Version: 3, Model: "deepseek/deepseek-v4.1-flash",
				OutputSchema: json.RawMessage(`{"type":"object"}`)},
			wantModel: llm.Spec{Model: "deepseek/deepseek-v4.1-flash", Temperature: &temp},
		},
		{
			name: "admin agent at the top level gets admin tools",
			spec: domain.SubagentSpec{Agent: "triager"},
			setup: func(src *agent.MockSource, ts *agent.MockToolset) {
				src.EXPECT().Get(mock.Anything, "triager").Return(stored(true), nil).Once()
				ts.EXPECT().For(domain.On("current_time"), true).Return(nil).Once()
			},
			wantInfo: agent.Info{Name: "triager", AgentID: id, Version: 3, Model: "openai/gpt-5-mini",
				OutputSchema: json.RawMessage(`{"type":"object"}`)},
			wantModel: llm.Spec{Model: "openai/gpt-5-mini", Temperature: &temp},
		},
		{
			name:  "admin agent as a subagent doesn't",
			spec:  domain.SubagentSpec{Agent: "triager"},
			depth: 1,
			setup: func(src *agent.MockSource, ts *agent.MockToolset) {
				src.EXPECT().Get(mock.Anything, "triager").Return(stored(true), nil).Once()
				ts.EXPECT().For(domain.On("current_time"), false).Return(nil).Once()
			},
			wantInfo: agent.Info{Name: "triager", AgentID: id, Version: 3, Model: "openai/gpt-5-mini",
				OutputSchema: json.RawMessage(`{"type":"object"}`)},
			wantModel: llm.Spec{Model: "openai/gpt-5-mini", Temperature: &temp},
		},
		{
			name:  "ad-hoc worker",
			spec:  domain.SubagentSpec{Model: "fake/worker", Tools: domain.On("current_time")},
			depth: 1,
			setup: func(_ *agent.MockSource, ts *agent.MockToolset) {
				ts.EXPECT().For(domain.On("current_time"), false).Return(nil).Once()
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

func TestEnsureAdmin(t *testing.T) {
	t.Parallel()

	seeder := agent.NewMockSeeder(t)
	seeder.EXPECT().Ensure(mock.Anything, agent.AdminName, mock.Anything, true,
		mock.MatchedBy(func(v domain.AgentVersionInput) bool {
			cfg, err := domain.ParseAgentConfig(v.Config)
			if err != nil {
				return false
			}
			return v.Model == "openai/gpt-5" && v.SystemPrompt != "" &&
				cfg.MaxIterations == 30 && assert.ObjectsAreEqual(domain.On(agent.AdminTools...), cfg.Tools)
		})).Return(nil).Once()

	require.NoError(t, agent.EnsureAdmin(context.Background(), seeder, "openai/gpt-5"))
}
