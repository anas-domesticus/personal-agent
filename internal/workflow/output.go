package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// CompileSchema checks that raw is a valid JSON Schema describing an object.
func CompileSchema(raw []byte) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("output schema is not valid JSON: %w", err)
	}
	if m, ok := doc.(map[string]any); !ok {
		return nil, errors.New("output schema must be a JSON object")
	} else if t, ok := m["type"]; ok && t != "object" {
		return nil, errors.New(`output schema must have "type": "object"`)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("output.json", doc); err != nil {
		return nil, err
	}
	s, err := c.Compile("output.json")
	if err != nil {
		return nil, fmt.Errorf("invalid output schema: %w", err)
	}
	return s, nil
}

// ParseOutput turns an agent's final reply into a node output. With no schema
// the reply is wrapped as {"text": ...}. With a schema, the reply must contain
// a JSON object that validates against it; surrounding prose and code fences
// are tolerated.
func ParseOutput(reply string, schema []byte) (json.RawMessage, error) {
	if len(schema) == 0 {
		return json.Marshal(map[string]string{"text": reply})
	}
	s, err := CompileSchema(schema)
	if err != nil {
		return nil, err
	}

	start, end := strings.Index(reply, "{"), strings.LastIndex(reply, "}")
	if start < 0 || end < start {
		return nil, errors.New("reply does not contain a JSON object")
	}
	raw := reply[start : end+1]

	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("reply is not valid JSON: %w", err)
	}
	if err := s.Validate(inst); err != nil {
		return nil, fmt.Errorf("reply does not match the output schema: %w", err)
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(raw)); err != nil {
		return nil, err
	}
	return compact.Bytes(), nil
}
