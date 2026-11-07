package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// AgentConfig is an agent version's settings. Unknown keys are rejected so a
// typo can't be silently ignored. It marshals to compact JSON with tools in
// name order.
type AgentConfig struct {
	// MaxIterations caps model calls in one turn. 0 means the default.
	MaxIterations int `json:"max_iterations,omitempty"`
	// Temperature and MaxTokens are passed to the model when set.
	Temperature *float32 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	// Tools turns tools on and off and configures them.
	Tools ToolConfig `json:"tools,omitempty"`
}

// ToolConfig maps a tool name to true (on, default settings), false (off),
// or a settings object (on, with those settings). Tools that aren't listed
// are off.
type ToolConfig map[string]json.RawMessage

var (
	jsonTrue  = json.RawMessage(`true`)
	jsonFalse = json.RawMessage(`false`)
)

// On returns a ToolConfig with each named tool set to true.
func On(names ...string) ToolConfig {
	c := ToolConfig{}
	for _, n := range names {
		c[n] = jsonTrue
	}
	return c
}

// State reports whether a tool is listed and, if so, whether it's on.
func (c ToolConfig) State(name string) (listed, on bool) {
	v, ok := c[name]
	if !ok {
		return false, false
	}
	return true, !bytes.Equal(v, jsonFalse)
}

// Enabled lists the tools that are on, in name order.
func (c ToolConfig) Enabled() []string {
	var out []string
	for name := range c {
		if _, on := c.State(name); on {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// Settings returns a tool's settings object, or nil if it is true or false.
func (c ToolConfig) Settings(name string) json.RawMessage {
	v := c[name]
	if len(v) > 0 && v[0] == '{' {
		return v
	}
	return nil
}

// normalize checks every value is true, false or an object, and compacts
// objects so stored config is canonical.
func (c ToolConfig) normalize() error {
	var errs []error
	for name, v := range c {
		v = bytes.TrimSpace(v)
		switch {
		case bytes.Equal(v, jsonTrue):
			c[name] = jsonTrue
		case bytes.Equal(v, jsonFalse):
			c[name] = jsonFalse
		case len(v) > 0 && v[0] == '{':
			var buf bytes.Buffer
			if err := json.Compact(&buf, v); err != nil {
				errs = append(errs, fmt.Errorf("tools.%s: %w", name, err))
				continue
			}
			c[name] = buf.Bytes()
		default:
			errs = append(errs, fmt.Errorf("tools.%s must be true, false or a settings object", name))
		}
	}
	return errors.Join(errs...)
}

// ParseAgentConfig decodes and validates a config object. Empty input is the
// zero config.
func ParseAgentConfig(raw []byte) (AgentConfig, error) {
	var c AgentConfig
	if len(bytes.TrimSpace(raw)) == 0 {
		return c, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("config: %w", err)
	}
	return c, c.Validate()
}

// Validate checks the config and normalises its tool values.
func (c AgentConfig) Validate() error {
	var errs []error
	if c.MaxIterations < 0 || c.MaxIterations > 100 {
		errs = append(errs, errors.New("config: max_iterations must be between 0 and 100"))
	}
	if c.Temperature != nil && (*c.Temperature < 0 || *c.Temperature > 2) {
		errs = append(errs, errors.New("config: temperature must be between 0 and 2"))
	}
	if c.MaxTokens != nil && *c.MaxTokens <= 0 {
		errs = append(errs, errors.New("config: max_tokens must be positive"))
	}
	if err := c.Tools.normalize(); err != nil {
		errs = append(errs, fmt.Errorf("config: %w", err))
	}
	return errors.Join(errs...)
}
