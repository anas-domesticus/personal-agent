// Package kit has the helpers every tool submodule uses.
package kit

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// Info describes a tool for listing.
type Info struct {
	Name        string `json:"name"`
	Description string `json:"description"`
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
	return utils.InferTool(name, desc, func(ctx context.Context, in T) (Result[D], error) {
		out, err := fn(ctx, in)
		if err != nil {
			if ctx.Err() != nil {
				return Result[D]{}, ctx.Err()
			}
			return Result[D]{Error: err.Error()}, nil
		}
		return Result[D]{OK: true, Data: &out}, nil
	})
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

type agentKey struct{}

// WithAgent records which agent is calling tools.
func WithAgent(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, agentKey{}, name)
}

// Agent returns the calling agent's name, or "".
func Agent(ctx context.Context) string {
	name, _ := ctx.Value(agentKey{}).(string)
	return name
}
