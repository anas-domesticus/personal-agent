// Package kit has the helpers every tool submodule uses.
package kit

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// MaxOutput caps what one tool call returns to the model, in characters
// (roughly 5k tokens). Tool results are kept in the conversation and replayed
// every turn, so one huge result would otherwise exhaust the context window
// for good.
const MaxOutput = 20000

// Truncate cuts a tool result to MaxOutput characters, saying so at the end.
func Truncate(out string) string {
	if len(out) <= MaxOutput || utf8.RuneCountInString(out) <= MaxOutput {
		return out
	}
	total := utf8.RuneCountInString(out)
	return string([]rune(out)[:MaxOutput]) + fmt.Sprintf(
		"\n...[truncated: the result was %d characters and only the first %d are shown. Ask for less, e.g. a narrower query or a smaller limit.]",
		total, MaxOutput)
}

// Capped wraps a tool so its output is truncated to MaxOutput.
func Capped(t tool.InvokableTool) tool.InvokableTool { return capped{t} }

type capped struct{ tool.InvokableTool }

func (c capped) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	out, err := c.InvokableTool.InvokableRun(ctx, args, opts...)
	return Truncate(out), err
}

// Name returns a tool's name.
func Name(t tool.BaseTool) string {
	info, err := t.Info(context.Background())
	if err != nil || info == nil {
		return ""
	}
	return info.Name
}

// Info describes a tool for listing.
type Info struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Settings describes the tool's settings object, if it takes one.
	Settings string `json:"settings,omitempty"`
}

// Result is what every tool returns to the model. Errors become results
// rather than Go errors so the model can read them and correct itself
// instead of the agent run failing.
type Result[D any] struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  *D     `json:"data,omitempty"`
}

// New builds a tool whose parameter schema is inferred from T. Use
// `jsonschema_description` tags for field descriptions; fields without
// `omitempty` are required.
func New[T, D any](name, desc string, fn func(context.Context, T) (D, error)) (tool.InvokableTool, error) {
	t, err := utils.InferTool(name, desc, func(ctx context.Context, in T) (Result[D], error) {
		out, err := fn(ctx, in)
		if err != nil {
			if ctx.Err() != nil {
				return Result[D]{}, ctx.Err()
			}
			return Result[D]{Error: err.Error()}, nil
		}
		return Result[D]{OK: true, Data: &out}, nil
	})
	if err != nil {
		return nil, fmt.Errorf("build tool %q: %w", name, err)
	}
	return Capped(t), nil
}

// Build runs each constructor and collects the tools.
func Build(fns ...func() (tool.InvokableTool, error)) ([]tool.InvokableTool, error) {
	out := make([]tool.InvokableTool, 0, len(fns))
	for _, fn := range fns {
		t, err := fn()
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// Limit clamps a requested result count to [1, 50], using def for zero.
func Limit(n, def int32) int32 {
	if n <= 0 {
		return def
	}
	return min(n, 50)
}
