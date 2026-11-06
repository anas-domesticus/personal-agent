package agent_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/chat"
	"github.com/anas-domesticus/personal-agent/internal/domain"
)

// toolCall is an assistant message calling one tool.
func toolCall(name, args string) *schema.Message {
	return schema.AssistantMessage("", []schema.ToolCall{{ID: "c1", Type: "function", Function: schema.FunctionCall{Name: name, Arguments: args}}})
}

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
	if err != nil {
		t.Fatal(err)
	}

	send := func(t *testing.T, args string) map[string]any {
		t.Helper()
		delegate = args
		turn, err := e.Chat.Send(ctx, conv.ID, "go", nil)
		if err != nil {
			t.Fatal(err)
		}
		var res map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(turn.Final, "result: ")), &res); err != nil {
			t.Fatalf("final = %q", turn.Final)
		}
		return res
	}

	var childID string
	t.Run("ad-hoc worker", func(t *testing.T) {
		res := send(t, `{"description": "check time", "prompt": "what time is it", "model": "fake/worker", "tools": ["current_time"]}`)
		data, _ := res["data"].(map[string]any)
		if res["ok"] != true || data["output"] != "worker did: what time is it" {
			t.Fatalf("result = %v", res)
		}
		childID = data["conversation_id"].(string)

		child, err := e.Chat.Get(ctx, mustUUID(t, childID))
		if err != nil {
			t.Fatal(err)
		}
		if child.ParentID == nil || *child.ParentID != conv.ID || child.Title != "check time" {
			t.Fatalf("child = %+v", child)
		}
		if s := child.Subagent; s == nil || s.Model != "fake/worker" || len(s.Tools) != 1 || s.Tools["current_time"] == nil {
			t.Fatalf("spec = %+v", child.Subagent)
		}
		h, _ := e.Chat.History(ctx, child.ID)
		if len(h) != 2 {
			t.Fatalf("child history = %d messages", len(h))
		}
		// Subagent conversations aren't top-level.
		list, _ := e.Chat.List(ctx, 50)
		for _, c := range list {
			if c.ID == child.ID {
				t.Fatal("subagent conversation listed as top-level")
			}
		}
	})

	t.Run("continue a subagent", func(t *testing.T) {
		res := send(t, `{"description": "follow up", "prompt": "and again", "conversation_id": "`+childID+`"}`)
		if res["ok"] != true {
			t.Fatalf("result = %v", res)
		}
		if h, _ := e.Chat.History(ctx, mustUUID(t, childID)); len(h) != 4 {
			t.Fatalf("child history = %d messages, want 4", len(h))
		}
	})

	t.Run("requests are checked", func(t *testing.T) {
		for args, want := range map[string]string{
			`{"description": "x", "prompt": "x", "model": "other/model"}`:                       "isn't allowed",
			`{"description": "x", "prompt": "x", "model": "fake/worker", "tools": ["shell"]}`:   "only have tools you have",
			`{"description": "x", "prompt": "x"}`:                                               "give agent, model",
			`{"description": "x", "prompt": "x", "agent": "parent", "tools": ["current_time"]}`: "only applies to ad-hoc",
		} {
			res := send(t, args)
			if res["ok"] != false || !strings.Contains(res["error"].(string), want) {
				t.Errorf("%s: got %v, want error containing %q", args, res, want)
			}
		}
	})

	t.Run("depth is limited", func(t *testing.T) {
		// A subagent of an agent with the default max_depth can't delegate.
		r, err := e.Builder.BuildSpec(ctx, domain.SubagentSpec{Agent: "parent"}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(r.Info().Tools, agent.SubagentToolName) {
			t.Fatalf("depth-1 subagent got the subagent tool: %v", r.Info().Tools)
		}
		top, err := e.Builder.Build(ctx, "parent")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(top.Info().Tools, agent.SubagentToolName) {
			t.Fatalf("top-level agent is missing the subagent tool: %v", top.Info().Tools)
		}
		// Subagents never get admin tools.
		admin, err := e.Builder.BuildSpec(ctx, domain.SubagentSpec{Agent: agent.AdminName}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(admin.Info().Tools, "create_agent") {
			t.Fatal("admin agent run as a subagent kept its admin tools")
		}
	})

	t.Run("stored agent on another model", func(t *testing.T) {
		ok(t)(e.Agents.Create(ctx, "helper", "x", domain.AgentVersionInput{SystemPrompt: "x", Model: "fake/parent"}))
		res := send(t, `{"description": "ask helper", "prompt": "hello", "agent": "helper", "model": "fake/worker"}`)
		data, _ := res["data"].(map[string]any)
		if res["ok"] != true || data["output"] != "worker did: hello" {
			t.Fatalf("result = %v", res)
		}
	})
}

func TestSubagentConversationsSurviveListing(t *testing.T) {
	// Window and Settled are shared by chat and subagents.
	msgs := []*schema.Message{schema.UserMessage("a"), toolCall("x", "{}")}
	if got := agent.Settled(msgs); len(got) != 1 {
		t.Fatalf("settled = %d", len(got))
	}
	var _ chat.Store = chat.NewPGStore(nil)
	var _ agent.SubagentStore = chat.NewPGStore(nil)
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
