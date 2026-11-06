package agent

// An internal test: message is how a streamed event becomes a message, and
// reaching it through Run would need a real Eino agent and model.

import (
	"errors"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
