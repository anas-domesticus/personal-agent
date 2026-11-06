package agent

import (
	"context"
	"errors"
	"fmt"
	"io"

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

func (b *built) Run(ctx context.Context, history []*schema.Message, obs Observer) (Turn, error) {
	ctx = domain.WithCaller(ctx, b.info.Name)
	if obs.Started != nil {
		obs.Started(b.info)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: b.agent, EnableStreaming: obs.Delta != nil})
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
		msg, err := message(ev.Output.MessageOutput, obs.Delta)
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
		if obs.Message != nil {
			obs.Message(msg)
		}
	}
	if len(t.Messages) == 0 {
		return t, errors.New("agent produced no output")
	}
	return t, nil
}

// message reads an event's message. A streamed assistant message is passed
// to delta chunk by chunk as it arrives, then joined.
func message(mv *adk.MessageVariant, delta func(*schema.Message)) (*schema.Message, error) {
	if !mv.IsStreaming || delta == nil || mv.Role != schema.Assistant {
		m, err := mv.GetMessage()
		if err != nil {
			return nil, fmt.Errorf("get message: %w", err)
		}
		return m, nil
	}
	s := mv.MessageStream
	defer s.Close()
	var chunks []*schema.Message
	for {
		c, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("receive stream: %w", err)
		}
		if c == nil {
			continue
		}
		chunks = append(chunks, c)
		if c.Content != "" || c.ReasoningContent != "" {
			delta(c)
		}
	}
	if len(chunks) == 0 {
		return nil, nil
	}
	m, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, fmt.Errorf("join streamed message: %w", err)
	}
	return m, nil
}
