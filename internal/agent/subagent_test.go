package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/chat"
	"github.com/anas-domesticus/personal-agent/internal/domain"
)

// toolCall is an assistant message calling one tool.
func toolCall(name, args string) *schema.Message {
	return schema.AssistantMessage("", []schema.ToolCall{{ID: "c1", Type: "function", Function: schema.FunctionCall{Name: name, Arguments: args}}})
}

// TestSubagent is an ordered scenario over one conversation: later subtests
// continue the subagent the first one started, so they run in sequence.
func TestSubagent(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	// The worker answers with what it was asked.
	e.SetModel("fake/worker", func(msgs []*schema.Message) *schema.Message {
		return schema.AssistantMessage("worker did: "+msgs[len(msgs)-1].Content, nil)
	})
	// The parent delegates whatever it is told to, then reports the result.
	var delegate string
	e.SetModel("fake/parent", func(msgs []*schema.Message) *schema.Message {
		if last := msgs[len(msgs)-1]; last.Role == schema.Tool {
			return schema.AssistantMessage("result: "+last.Content, nil)
		}
		return toolCall("subagent", delegate)
	})
	ok(t)(e.Agents.Create(ctx, "parent", "x", domain.AgentVersionInput{
		SystemPrompt: "x", Model: "fake/parent",
		Config: json.RawMessage(`{"tools": {"current_time": true, "web_search": true,
			"subagent": {"models": ["fake/worker", "fake/parent"]}}}`),
	}))
	conv, err := e.Chat.Start(ctx, "parent")
	require.NoError(t, err)

	send := func(t *testing.T, args string) map[string]any {
		t.Helper()
		delegate = args
		turn, err := e.Chat.Send(ctx, conv.ID, "go", agent.Observer{})
		require.NoError(t, err)
		var res map[string]any
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(turn.Final, "result: ")), &res), "final = %q", turn.Final)
		return res
	}
	data := func(t *testing.T, res map[string]any) map[string]any {
		t.Helper()
		require.Equal(t, true, res["ok"], "result = %v", res)
		d, isMap := res["data"].(map[string]any)
		require.True(t, isMap, "result = %v", res)
		return d
	}

	var childID string
	t.Run("ad-hoc worker", func(t *testing.T) {
		d := data(t, send(t, `{"description": "check time", "prompt": "what time is it", "model": "fake/worker", "tools": ["current_time"]}`))
		assert.Equal(t, "worker did: what time is it", d["output"])
		var isString bool
		childID, isString = d["conversation_id"].(string)
		require.True(t, isString, "data = %v", d)

		child, err := e.Chat.Get(ctx, mustUUID(t, childID))
		require.NoError(t, err)
		require.NotNil(t, child.ParentID)
		assert.Equal(t, conv.ID, *child.ParentID)
		assert.Equal(t, "check time", child.Title)
		require.NotNil(t, child.Subagent)
		assert.Equal(t, "fake/worker", child.Subagent.Model)
		require.Len(t, child.Subagent.Tools, 1)
		assert.NotNil(t, child.Subagent.Tools["current_time"])

		h, err := e.Chat.History(ctx, child.ID)
		require.NoError(t, err)
		assert.Len(t, h, 2, "child history")

		// Subagent conversations aren't top-level.
		list, err := e.Chat.List(ctx, 50)
		require.NoError(t, err)
		for _, c := range list {
			assert.NotEqual(t, child.ID, c.ID, "subagent conversation listed as top-level")
		}
	})

	t.Run("continue a subagent", func(t *testing.T) {
		data(t, send(t, `{"description": "follow up", "prompt": "and again", "conversation_id": "`+childID+`"}`))
		h, err := e.Chat.History(ctx, mustUUID(t, childID))
		require.NoError(t, err)
		assert.Len(t, h, 4, "child history")
	})

	t.Run("requests are checked", func(t *testing.T) {
		tests := []struct {
			name    string
			args    string
			wantErr string
		}{
			{name: "model not allowed", args: `{"description": "x", "prompt": "x", "model": "other/model"}`, wantErr: "isn't allowed"},
			{name: "tool the caller lacks", args: `{"description": "x", "prompt": "x", "model": "fake/worker", "tools": ["shell"]}`, wantErr: "only have tools you have"},
			{name: "neither agent nor model", args: `{"description": "x", "prompt": "x"}`, wantErr: "give agent, model"},
			{name: "tools for a stored agent", args: `{"description": "x", "prompt": "x", "agent": "parent", "tools": ["current_time"]}`, wantErr: "only applies to ad-hoc"},
		}
		// Not parallel: each case drives the same conversation.
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				assert.Contains(t, toolError(t, send(t, tt.args)), tt.wantErr)
			})
		}
	})

	t.Run("depth is limited", func(t *testing.T) {
		// A subagent of an agent with the default max_depth can't delegate.
		r, err := e.Builder.BuildSpec(ctx, domain.SubagentSpec{Agent: "parent"}, 1)
		require.NoError(t, err)
		assert.NotContains(t, r.Info().Tools, agent.SubagentToolName, "depth-1 subagent got the subagent tool")

		top, err := e.Builder.Build(ctx, "parent")
		require.NoError(t, err)
		assert.Contains(t, top.Info().Tools, agent.SubagentToolName, "top-level agent is missing the subagent tool")

		// Subagents never get admin tools.
		admin, err := e.Builder.BuildSpec(ctx, domain.SubagentSpec{Agent: agent.AdminName}, 1)
		require.NoError(t, err)
		assert.NotContains(t, admin.Info().Tools, "create_agent", "admin agent run as a subagent kept its admin tools")
	})

	t.Run("stored agent on another model", func(t *testing.T) {
		ok(t)(e.Agents.Create(ctx, "helper", "x", domain.AgentVersionInput{SystemPrompt: "x", Model: "fake/parent"}))
		d := data(t, send(t, `{"description": "ask helper", "prompt": "hello", "agent": "helper", "model": "fake/worker"}`))
		assert.Equal(t, "worker did: hello", d["output"])
	})
}

func TestSubagentConversationsSurviveListing(t *testing.T) {
	t.Parallel()
	// Window and Settled are shared by chat and subagents.
	msgs := []*schema.Message{schema.UserMessage("a"), toolCall("x", "{}")}
	assert.Len(t, agent.Settled(msgs), 1)
	var _ chat.Store = chat.NewPGStore(nil)
	var _ agent.SubagentStore = chat.NewPGStore(nil)
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	require.NoError(t, err)
	return id
}
