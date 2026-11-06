package workflow

import (
	"strings"
	"testing"
)

const rankSchema = `{"type":"object","properties":{"ranked":{"type":"array","items":{"type":"string"}}},"required":["ranked"]}`

func TestParseOutput(t *testing.T) {
	tests := []struct {
		name, reply, schema, want, wantErr string
	}{
		{name: "no schema wraps text", reply: "hello", want: `{"text":"hello"}`},
		{name: "plain json", reply: `{"ranked": ["a", "b"]}`, schema: rankSchema, want: `{"ranked":["a","b"]}`},
		{name: "fenced json with prose", reply: "Here you go:\n```json\n{\"ranked\": [\"a\"]}\n```", schema: rankSchema, want: `{"ranked":["a"]}`},
		{name: "missing required", reply: `{"other": 1}`, schema: rankSchema, wantErr: "does not match"},
		{name: "wrong type", reply: `{"ranked": "a"}`, schema: rankSchema, wantErr: "does not match"},
		{name: "no object", reply: "sorry", schema: rankSchema, wantErr: "does not contain a JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseOutput(tt.reply, []byte(tt.schema))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestCompileSchemaRejects(t *testing.T) {
	for _, s := range []string{`[]`, `{"type":"string"}`, `{"type":"object","properties":{"a":{"type":"nope"}}}`} {
		if _, err := CompileSchema([]byte(s)); err == nil {
			t.Errorf("expected %s to be rejected", s)
		}
	}
}
