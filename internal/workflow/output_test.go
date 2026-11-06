package workflow_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

const rankSchema = `{"type":"object","properties":{"ranked":{"type":"array","items":{"type":"string"}}},"required":["ranked"]}`

func TestParseOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		reply   string
		schema  string
		want    string
		wantErr string // substring; empty means no error
	}{
		{name: "no schema wraps text", reply: "hello", want: `{"text":"hello"}`},
		{name: "plain json", reply: `{"ranked": ["a", "b"]}`, schema: rankSchema, want: `{"ranked":["a","b"]}`},
		{name: "fenced json with prose", reply: "Here you go:\n```json\n{\"ranked\": [\"a\"]}\n```", schema: rankSchema, want: `{"ranked":["a"]}`},
		{name: "missing required", reply: `{"other": 1}`, schema: rankSchema, wantErr: "does not match"},
		{name: "wrong type", reply: `{"ranked": "a"}`, schema: rankSchema, wantErr: "does not match"},
		{name: "no object", reply: "sorry", schema: rankSchema, wantErr: "does not contain a JSON object"},
		{name: "invalid json object", reply: `{"ranked": [}`, schema: rankSchema, wantErr: "reply is not valid JSON"},
		{name: "invalid schema", reply: `{}`, schema: `[]`, wantErr: "must be a JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := workflow.ParseOutput(tt.reply, []byte(tt.schema))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
		})
	}
}

func TestCompileSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		schema  string
		wantErr string // substring; empty means no error
	}{
		{name: "object schema", schema: rankSchema},
		{name: "object schema without a type", schema: `{"properties":{"a":{"type":"string"}}}`},
		{name: "not json", schema: `{`, wantErr: "not valid JSON"},
		{name: "array", schema: `[]`, wantErr: "must be a JSON object"},
		{name: "string type", schema: `{"type":"string"}`, wantErr: `"type": "object"`},
		{name: "invalid property type", schema: `{"type":"object","properties":{"a":{"type":"nope"}}}`, wantErr: "invalid output schema"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := workflow.CompileSchema([]byte(tt.schema))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}
