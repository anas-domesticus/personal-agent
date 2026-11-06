package kit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
)

// Spec describes a tool that agent versions can enable in their config's
// "tools" object, and how to build it.
//
// Each tool submodule exports its Specs; the tools registry collects them.
type Spec struct {
	// Name is the key in an agent config's "tools" object.
	Name string
	// Summary is one line for list_tools.
	Summary string
	// Admin tools are only given to admin agents.
	Admin bool
	// Settings returns a pointer to a new zero value of the tool's settings,
	// or is nil if the tool takes none. Settings are decoded strictly.
	Settings func() any
	// SettingsHelp describes the settings object for list_tools.
	SettingsHelp string
	// New builds the tool. settings is the decoded value from Settings, or
	// nil when the tool takes none or the config gave "true".
	New func(settings any) (tool.InvokableTool, error)
}

// DecodeSettings decodes a tool's settings value: true, or an object. It
// returns nil settings for true.
func (s Spec) DecodeSettings(raw json.RawMessage) (any, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("true")) {
		if s.Settings == nil {
			return nil, nil
		}
		return s.Settings(), nil // defaults
	}
	if s.Settings == nil {
		return nil, fmt.Errorf("tool %q takes no settings; use true", s.Name)
	}
	v := s.Settings()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return nil, fmt.Errorf("tool %q settings: %w", s.Name, err)
	}
	if val, ok := v.(interface{ Validate() error }); ok {
		if err := val.Validate(); err != nil {
			return nil, fmt.Errorf("tool %q settings: %w", s.Name, err)
		}
	}
	return v, nil
}

// Static is a Spec for a tool that takes no settings.
func Static(summary string, t tool.InvokableTool) Spec {
	return Spec{
		Name:    Name(t),
		Summary: summary,
		New:     func(any) (tool.InvokableTool, error) { return t, nil },
	}
}

// Specs builds each tool and wraps it as a settings-less Spec, using the
// tool's description as its summary.
func Specs(ts []tool.InvokableTool, err error) ([]Spec, error) {
	if err != nil {
		return nil, err
	}
	out := make([]Spec, len(ts))
	for i, t := range ts {
		out[i] = Static(firstLine(t), t)
	}
	return out, nil
}

func firstLine(t tool.InvokableTool) string {
	info, err := t.Info(context.Background())
	if err != nil || info == nil {
		return ""
	}
	desc := info.Desc
	if i := bytes.IndexByte([]byte(desc), '\n'); i >= 0 {
		desc = desc[:i]
	}
	return desc
}
