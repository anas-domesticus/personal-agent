package agent

// Internal tests cover built.Run through its private runner seam and the
// streamed event-to-message conversion that is otherwise Eino-owned.

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
)

// fakeTurnRunner plays back events because Eino runner expectations would be
// more brittle than the small iterator contract built.Run consumes.
type fakeTurnRunner struct {
	events  []*adk.AgentEvent
	history []*schema.Message
	caller  string
}

var _ turnRunner = (*fakeTurnRunner)(nil)

func (r *fakeTurnRunner) Run(ctx context.Context, history []*schema.Message) eventIterator {
	r.history = history
	r.caller = domain.Caller(ctx)
	return &sliceEventIterator{events: r.events}
}

type sliceEventIterator struct {
	events []*adk.AgentEvent
}

func (i *sliceEventIterator) Next() (*adk.AgentEvent, bool) {
	if len(i.events) == 0 {
		return nil, false
	}
	ev := i.events[0]
	i.events = i.events[1:]
	return ev, true
}

func messageEvent(msg *schema.Message) *adk.AgentEvent {
	return &adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		Message: msg,
		Role:    msg.Role,
	}}}
}

func TestBuiltRun(t *testing.T) {
	t.Parallel()

	info := Info{Name: "researcher", Model: "fake/model"}
	first := schema.AssistantMessage("working", nil)
	first.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 4}}
	toolResult := schema.ToolMessage("result", "call-1")
	last := schema.AssistantMessage("done", nil)
	last.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 20, CompletionTokens: 6}}
	runErr := errors.New("model disconnected")

	tests := []struct {
		name       string
		events     []*adk.AgentEvent
		streaming  bool
		want       Turn
		wantErr    error
		wantErrMsg string
	}{
		{
			name: "collects messages usage and observers while Eino owns execution",
			events: []*adk.AgentEvent{
				{},
				{Output: &adk.AgentOutput{}},
				messageEvent(first),
				messageEvent(toolResult),
				messageEvent(last),
			},
			streaming: true,
			want: Turn{
				Messages:         []*schema.Message{first, toolResult, last},
				Final:            "done",
				PromptTokens:     30,
				CompletionTokens: 10,
			},
		},
		{
			name:   "returns a partial turn when execution fails",
			events: []*adk.AgentEvent{messageEvent(first), {Err: runErr}},
			want: Turn{
				Messages:         []*schema.Message{first},
				Final:            "working",
				PromptTokens:     10,
				CompletionTokens: 4,
			},
			wantErr: runErr,
		},
		{
			name:       "rejects an empty turn",
			events:     []*adk.AgentEvent{{}},
			wantErrMsg: "agent produced no output",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeTurnRunner{events: tt.events}
			var gotConfig adk.RunnerConfig
			b := &built{
				info: info,
				newRunner: func(_ context.Context, cfg adk.RunnerConfig) turnRunner {
					gotConfig = cfg
					return runner
				},
			}
			var started []Info
			var observed []*schema.Message
			obs := Observer{
				Started: func(got Info) { started = append(started, got) },
				Message: func(msg *schema.Message) { observed = append(observed, msg) },
			}
			if tt.streaming {
				obs.Delta = func(*schema.Message) {}
			}
			history := []*schema.Message{schema.UserMessage("investigate")}

			got, err := b.Run(context.Background(), history, obs)

			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want.Messages, observed)
			assert.Equal(t, []Info{info}, started)
			assert.Equal(t, tt.streaming, gotConfig.EnableStreaming)
			assert.Equal(t, history, runner.history)
			assert.Equal(t, info.Name, runner.caller)
			assert.Equal(t, info, b.Info())
			switch {
			case tt.wantErr != nil:
				require.ErrorIs(t, err, tt.wantErr)
				require.ErrorContains(t, err, "agent researcher")
			case tt.wantErrMsg != "":
				require.EqualError(t, err, tt.wantErrMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestMessage(t *testing.T) {
	t.Parallel()
	errStream := errors.New("connection reset")

	// stream sends chunks, then err if set, then ends.
	stream := func(err error, chunks ...*schema.Message) *schema.StreamReader[*schema.Message] {
		r, w := schema.Pipe[*schema.Message](len(chunks) + 1)
		for _, c := range chunks {
			w.Send(c, nil)
		}
		if err != nil {
			w.Send(nil, err)
		}
		w.Close()
		return r
	}
	assistant := func(content string) *schema.Message { return &schema.Message{Role: schema.Assistant, Content: content} }

	tests := []struct {
		name       string
		mv         func() *adk.MessageVariant
		noDelta    bool // stream without a Delta observer
		want       *schema.Message
		wantDeltas []string
		wantErr    error
	}{
		{
			name: "whole message",
			mv: func() *adk.MessageVariant {
				return &adk.MessageVariant{Message: assistant("hi"), Role: schema.Assistant}
			},
			want: assistant("hi"),
		},
		{
			name: "streamed message is passed on chunk by chunk, then joined",
			mv: func() *adk.MessageVariant {
				return &adk.MessageVariant{IsStreaming: true, Role: schema.Assistant,
					MessageStream: stream(nil, assistant("Hel"), assistant(""), assistant("lo"))}
			},
			want:       assistant("Hello"),
			wantDeltas: []string{"Hel", "lo"}, // the empty chunk isn't passed on
		},
		{
			name: "streamed without an observer is joined",
			mv: func() *adk.MessageVariant {
				return &adk.MessageVariant{IsStreaming: true, Role: schema.Assistant, MessageStream: stream(nil, assistant("a"), assistant("b"))}
			},
			noDelta: true,
			want:    assistant("ab"),
		},
		{
			name: "streamed tool result is joined, not passed on",
			mv: func() *adk.MessageVariant {
				return &adk.MessageVariant{IsStreaming: true, Role: schema.Tool,
					MessageStream: stream(nil, &schema.Message{Role: schema.Tool, Content: "out"})}
			},
			want: &schema.Message{Role: schema.Tool, Content: "out"},
		},
		{
			name: "a stream that breaks fails",
			mv: func() *adk.MessageVariant {
				return &adk.MessageVariant{IsStreaming: true, Role: schema.Assistant, MessageStream: stream(errStream, assistant("par"))}
			},
			wantDeltas: []string{"par"},
			wantErr:    errStream,
		},
		{
			name: "an empty stream is no message",
			mv: func() *adk.MessageVariant {
				return &adk.MessageVariant{IsStreaming: true, Role: schema.Assistant, MessageStream: stream(nil)}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var deltas []string
			delta := func(c *schema.Message) { deltas = append(deltas, c.Content) }
			if tt.noDelta {
				delta = nil
			}

			got, err := message(tt.mv(), delta)

			assert.Equal(t, tt.wantDeltas, deltas)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.want.Role, got.Role)
			assert.Equal(t, tt.want.Content, got.Content)
		})
	}
}
