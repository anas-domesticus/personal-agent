package chat_test

import (
	"context"
	"os"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/llm"
	"github.com/anas-domesticus/personal-agent/internal/runs"
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

	e := testenv.NewWithModels(t, slug, func(ctx context.Context, s string) (model.ToolCallingChatModel, error) {
		return llm.NewChatModel(ctx, llm.Config{APIKey: key}, s)
	})

	id, err := e.Chat.Start(ctx, agent.AdminName)
	if err != nil {
		t.Fatal(err)
	}
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

	runsList, err := e.Catalog.ListRuns(ctx, "greet", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runsList) == 0 {
		t.Fatal("admin did not run the workflow")
	}
	r, err := e.Catalog.GetRun(ctx, runsList[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Run.Status != runs.StatusSucceeded {
		t.Fatalf("run %s: %v", r.Run.Status, r.Run.Error)
	}
	t.Logf("run output: %s", r.Run.Output)
}
