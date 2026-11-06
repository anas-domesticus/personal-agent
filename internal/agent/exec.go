package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/anas-domesticus/personal-agent/internal/domain"
)

// Turn is what one agent invocation produced.
type Turn struct {
	// Messages are the assistant and tool messages produced, in order.
	Messages         []*schema.Message
	Final            string // content of the last assistant message
	PromptTokens     int
	CompletionTokens int
}

type built struct {
	agent adk.Agent
	info  Info
}

func (b *built) Info() Info { return b.info }

func (b *built) Run(ctx context.Context, history []*schema.Message, onMessage func(*schema.Message)) (Turn, error) {
	ctx = domain.WithCaller(ctx, b.info.Name)
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: b.agent})
	events := runner.Run(ctx, history)

	var t Turn
	for {
		ev, ok := events.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			return t, fmt.Errorf("agent %s: %w", b.info.Name, ev.Err)
		}
		if ev.Output == nil || ev.Output.MessageOutput == nil {
			continue
		}
		msg, err := ev.Output.MessageOutput.GetMessage()
		if err != nil {
			return t, fmt.Errorf("agent %s: read message: %w", b.info.Name, err)
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
