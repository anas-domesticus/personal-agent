package agent

import "github.com/cloudwego/eino/schema"

// DefaultHistoryChars is how much history a turn sees by default, in
// characters (roughly 50k tokens).
const DefaultHistoryChars = 200_000

// Window returns the most recent messages that fit in maxChars. It always
// starts at a user message, so a tool result is never sent without the call
// that asked for it, and it always includes the last user message.
func Window(msgs []*schema.Message, maxChars int) []*schema.Message {
	if maxChars <= 0 {
		return msgs
	}
	start, used := len(msgs), 0
	for i := len(msgs) - 1; i >= 0; i-- {
		used += size(msgs[i])
		if used > maxChars {
			break
		}
		start = i
	}
	// Move forward to a user message.
	for start < len(msgs) && msgs[start].Role != schema.User {
		start++
	}
	if start == len(msgs) {
		// Even the last turn doesn't fit; send from its user message and let
		// the model's limit decide.
		for start = len(msgs) - 1; start > 0 && msgs[start].Role != schema.User; start-- {
		}
	}
	return msgs[start:]
}

func size(m *schema.Message) int {
	n := len(m.Content)
	for _, tc := range m.ToolCalls {
		n += len(tc.Function.Name) + len(tc.Function.Arguments)
	}
	return n
}

// Settled returns the longest prefix of msgs with no tool call awaiting its
// result.
func Settled(msgs []*schema.Message) []*schema.Message {
	pending := map[string]bool{}
	end := 0
	for i, m := range msgs {
		switch m.Role {
		case schema.Assistant:
			for _, tc := range m.ToolCalls {
				pending[tc.ID] = true
			}
		case schema.Tool:
			delete(pending, m.ToolCallID)
		}
		if len(pending) == 0 {
			end = i + 1
		}
	}
	return msgs[:end]
}

func ranTools(msgs []*schema.Message) bool {
	for _, m := range msgs {
		if m.Role == schema.Tool {
			return true
		}
	}
	return false
}
