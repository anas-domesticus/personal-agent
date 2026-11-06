package chat_test

import (
	"context"
	"os"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/llm"
	"github.com/anas-domesticus/personal-agent/internal/search"
	"github.com/anas-domesticus/personal-agent/internal/testenv"
)

// TestLiveAdmin drives the real admin model through OpenRouter: it must build
// an agent with structured output, wrap it in a workflow, run it and report
// back. Runs only when OPENROUTER_API_KEY is set; costs a fraction of a cent.
func TestLiveAdmin(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	slug := os.Getenv("ADMIN_MODEL")
	if slug == "" {
		slug = "deepseek/deepseek-v4.1-flash"
	}
	ctx := context.Background()

	e := testenv.NewWith(t, slug, testenv.Options{Models: func(ctx context.Context, s llm.Spec) (model.ToolCallingChatModel, error) {
		return llm.NewChatModel(ctx, llm.Config{APIKey: key}, s)
	}})

	conv, err := e.Chat.Start(ctx, agent.AdminName)
	if err != nil {
		t.Fatal(err)
	}
	id := conv.ID
	prompt := "Create an agent named greeter (model " + slug + ") that writes a one-sentence greeting for a person. " +
		`Give it an output schema of {"type":"object","properties":{"greeting":{"type":"string"}},"required":["greeting"]}. ` +
		"Then create a workflow named greet with a single node using it, run it with input {\"name\": \"Lewis\"} waiting up to 120 seconds, " +
		"and tell me the greeting it produced. Don't ask me to confirm; just do it."

	turn, err := e.Chat.Send(ctx, id, prompt, func(m *schema.Message) {
		for _, tc := range m.ToolCalls {
			t.Logf("tool call: %s %s", tc.Function.Name, tc.Function.Arguments)
		}
		if m.Role == schema.Tool {
			t.Logf("tool result: %.300s", m.Content)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("final: %s", turn.Final)
	t.Logf("tokens: prompt=%d completion=%d", turn.PromptTokens, turn.CompletionTokens)

	runsList, err := e.Runs.List(ctx, "greet", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runsList) == 0 {
		t.Fatal("admin did not run the workflow")
	}
	r, err := e.Runs.Get(ctx, runsList[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != domain.RunSucceeded {
		t.Fatalf("run %s: %v", r.Status, r.Error)
	}
	t.Logf("run output: %s", r.Output)
}

// TestLiveSubagentSearch has the admin delegate a web search to an ad-hoc
// subagent on a named model, using OpenRouter's real web search.
func TestLiveSubagentSearch(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	slug := "deepseek/deepseek-v4.1-flash"
	ctx := context.Background()
	e := testenv.NewWith(t, slug, testenv.Options{
		Models: func(ctx context.Context, s llm.Spec) (model.ToolCallingChatModel, error) {
			return llm.NewChatModel(ctx, llm.Config{APIKey: key}, s)
		},
		Search: search.NewOpenRouter(search.OpenRouterConfig{APIKey: key, Model: slug}),
	})
	conv, err := e.Chat.Start(ctx, agent.AdminName)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := e.Chat.Send(ctx, conv.ID,
		"Use the subagent tool with model "+slug+" and only the web_search tool to find the latest stable Go release version. "+
			"Then tell me the version. Don't ask me anything.",
		func(m *schema.Message) {
			for _, tc := range m.ToolCalls {
				t.Logf("tool call: %s %.300s", tc.Function.Name, tc.Function.Arguments)
			}
		})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("final: %s", turn.Final)

	var child *domain.Conversation
	var count int
	if err := e.Store.Pool.QueryRow(ctx, `select count(*) from conversations where parent_id = $1`, conv.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("admin did not start a subagent")
	}
	var childID uuid.UUID
	if err := e.Store.Pool.QueryRow(ctx, `select id from conversations where parent_id = $1 limit 1`, conv.ID).Scan(&childID); err != nil {
		t.Fatal(err)
	}
	c, err := e.Chat.Get(ctx, childID)
	if err != nil {
		t.Fatal(err)
	}
	child = &c
	h, err := e.Chat.History(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	searched := false
	for _, m := range h {
		for _, tc := range m.ToolCalls {
			searched = searched || tc.Function.Name == "web_search"
		}
		if m.Role == schema.Tool {
			t.Logf("subagent tool result: %.400s", m.Content)
		}
	}
	if !searched {
		t.Fatalf("subagent (%+v) didn't search the web", child.Subagent)
	}
}
