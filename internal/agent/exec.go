package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

// Turn is what one agent invocation produced.
type Turn struct {
	// Messages are the assistant and tool messages produced, in order.
	Messages         []*schema.Message
	Final            string // content of the last assistant message
	PromptTokens     int
	CompletionTokens int
}

// Run sends the history to the agent and collects everything it produces.
// onMessage, if set, is called with each message as it completes.
func (b *Built) Run(ctx context.Context, history []*schema.Message, onMessage func(*schema.Message)) (Turn, error) {
	ctx = kit.WithAgent(ctx, b.Name)
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: b.Agent})
	events := runner.Run(ctx, history)

	var t Turn
	for {
		ev, ok := events.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			return t, fmt.Errorf("agent %s: %w", b.Name, ev.Err)
		}
		if ev.Output == nil || ev.Output.MessageOutput == nil {
			continue
		}
		msg, err := ev.Output.MessageOutput.GetMessage()
		if err != nil {
			return t, fmt.Errorf("agent %s: read message: %w", b.Name, err)
		}
		if msg == nil {
			continue
		}
		t.Messages = append(t.Messages, msg)
		if msg.Role == schema.Assistant {
			t.Final = msg.Content
			if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
				t.PromptTokens += msg.ResponseMeta.Usage.PromptTokens
				t.CompletionTokens += msg.ResponseMeta.Usage.CompletionTokens
			}
		}
		if onMessage != nil {
			onMessage(msg)
		}
	}
	if len(t.Messages) == 0 {
		return t, errors.New("agent produced no output")
	}
	return t, nil
}
