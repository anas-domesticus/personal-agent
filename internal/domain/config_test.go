package domain_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
)

func ptr[T any](v T) *T { return &v }

func TestParseAgentConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in       string
		want     domain.AgentConfig
		wantJSON string // canonical encoding of want
		wantErr  string // substring; empty means no error
	}{
		{name: "empty input", in: ``, want: domain.AgentConfig{}, wantJSON: `{}`},
		{name: "blank input", in: "  \n", want: domain.AgentConfig{}, wantJSON: `{}`},
		{name: "empty object", in: `{}`, want: domain.AgentConfig{}, wantJSON: `{}`},
		{
			name: "every setting",
			in:   `{"max_iterations": 5, "temperature": 0.5, "max_tokens": 100, "tools": {"b": false, "a": true}}`,
			want: domain.AgentConfig{
				MaxIterations: 5, Temperature: ptr[float32](0.5), MaxTokens: ptr(100),
				Tools: domain.ToolConfig{"a": json.RawMessage(`true`), "b": json.RawMessage(`false`)},
			},
			wantJSON: `{"max_iterations":5,"temperature":0.5,"max_tokens":100,"tools":{"a":true,"b":false}}`,
		},
		{
			name:     "tool settings are compacted",
			in:       `{"tools": {"web_search": { "max_results" : 3,  "site": "x" }}}`,
			want:     domain.AgentConfig{Tools: domain.ToolConfig{"web_search": json.RawMessage(`{"max_results":3,"site":"x"}`)}},
			wantJSON: `{"tools":{"web_search":{"max_results":3,"site":"x"}}}`,
		},
		{name: "upper bounds", in: `{"max_iterations": 100, "temperature": 2}`,
			want:     domain.AgentConfig{MaxIterations: 100, Temperature: ptr[float32](2)},
			wantJSON: `{"max_iterations":100,"temperature":2}`},
		{name: "lower bounds", in: `{"max_iterations": 0, "temperature": 0, "max_tokens": 1}`,
			want:     domain.AgentConfig{Temperature: ptr[float32](0), MaxTokens: ptr(1)},
			wantJSON: `{"temperature":0,"max_tokens":1}`},
		{name: "not json", in: `{`, wantErr: "config:"},
		{name: "not an object", in: `[]`, wantErr: "config:"},
		{name: "unknown key", in: `{"nope": 1}`, wantErr: `unknown field "nope"`},
		{name: "unknown key in a typo", in: `{"max_iteration": 1}`, wantErr: "unknown field"},
		{name: "negative max iterations", in: `{"max_iterations": -1}`, wantErr: "max_iterations must be between 0 and 100"},
		{name: "too many max iterations", in: `{"max_iterations": 101}`, wantErr: "max_iterations must be between 0 and 100"},
		{name: "negative temperature", in: `{"temperature": -0.1}`, wantErr: "temperature must be between 0 and 2"},
		{name: "temperature too high", in: `{"temperature": 2.1}`, wantErr: "temperature must be between 0 and 2"},
		{name: "zero max tokens", in: `{"max_tokens": 0}`, wantErr: "max_tokens must be positive"},
		{name: "tool set to a number", in: `{"tools": {"a": 1}}`, wantErr: "tools.a must be true, false or a settings object"},
		{name: "tool set to a string", in: `{"tools": {"a": "on"}}`, wantErr: "tools.a must be true, false or a settings object"},
		{name: "tool set to null", in: `{"tools": {"a": null}}`, wantErr: "tools.a must be true, false or a settings object"},
		{name: "tool set to an array", in: `{"tools": {"a": []}}`, wantErr: "tools.a must be true, false or a settings object"},
		{name: "every error is reported", in: `{"max_iterations": -1, "max_tokens": -1}`, wantErr: "max_iterations must be between 0 and 100\nconfig: max_tokens must be positive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.ParseAgentConfig([]byte(tt.in))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			raw, err := json.Marshal(got)
			require.NoError(t, err)
			assert.Equal(t, tt.wantJSON, string(raw), "canonical compact JSON")
		})
	}
}

func TestAgentConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cfg       domain.AgentConfig
		wantTools domain.ToolConfig
		wantErr   string // substring; empty means no error
	}{
		{name: "zero value", cfg: domain.AgentConfig{}},
		{name: "normalises tool values in place",
			cfg: domain.AgentConfig{Tools: domain.ToolConfig{
				"a": json.RawMessage(" true "), "b": json.RawMessage("\tfalse"), "c": json.RawMessage(`{ "x" : [1, 2] }`),
			}},
			wantTools: domain.ToolConfig{
				"a": json.RawMessage(`true`), "b": json.RawMessage(`false`), "c": json.RawMessage(`{"x":[1,2]}`),
			}},
		{name: "malformed settings object",
			cfg:     domain.AgentConfig{Tools: domain.ToolConfig{"a": json.RawMessage(`{"x":`)}},
			wantErr: "config: tools.a:"},
		{name: "empty tool value",
			cfg:     domain.AgentConfig{Tools: domain.ToolConfig{"a": json.RawMessage(``)}},
			wantErr: "tools.a must be true, false or a settings object"},
		{name: "max iterations out of range", cfg: domain.AgentConfig{MaxIterations: 101}, wantErr: "max_iterations"},
		{name: "temperature out of range", cfg: domain.AgentConfig{Temperature: ptr[float32](3)}, wantErr: "temperature"},
		{name: "max tokens not positive", cfg: domain.AgentConfig{MaxTokens: ptr(-5)}, wantErr: "max_tokens"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.cfg.Validate()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantTools, tt.cfg.Tools)
		})
	}
}

func TestOn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		names []string
		want  domain.ToolConfig
	}{
		{name: "no names", want: domain.ToolConfig{}},
		{name: "several names", names: []string{"a", "b"},
			want: domain.ToolConfig{"a": json.RawMessage(`true`), "b": json.RawMessage(`true`)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, domain.On(tt.names...))
		})
	}
}

func TestToolConfig(t *testing.T) {
	t.Parallel()
	c := domain.ToolConfig{
		"on":       json.RawMessage(`true`),
		"off":      json.RawMessage(`false`),
		"settings": json.RawMessage(`{"max_results":3}`),
	}

	t.Run("enabled lists tools that are on, sorted", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"on", "settings"}, c.Enabled())
	})

	t.Run("enabled is empty for an empty config", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, domain.ToolConfig{}.Enabled())
	})

	tests := []struct {
		name         string
		tool         string
		wantListed   bool
		wantOn       bool
		wantSettings json.RawMessage
	}{
		{name: "true", tool: "on", wantListed: true, wantOn: true},
		{name: "false", tool: "off", wantListed: true},
		{name: "settings object", tool: "settings", wantListed: true, wantOn: true,
			wantSettings: json.RawMessage(`{"max_results":3}`)},
		{name: "not listed", tool: "missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			listed, on := c.State(tt.tool)
			assert.Equal(t, tt.wantListed, listed, "listed")
			assert.Equal(t, tt.wantOn, on, "on")
			assert.Equal(t, tt.wantSettings, c.Settings(tt.tool))
		})
	}
}
