// Package knowledge defines the versioned knowledge primitives shared by the
// application.
package knowledge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

// Stable knowledge node kinds.
const (
	ObjectiveKind = "objective"
	WorkflowKind  = "workflow"
	ClaimKind     = "claim"
	SourceKind    = "source"
	AgentKind     = "agent"
)

// Objective describes a desired outcome and its success criteria.
type Objective struct {
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	SuccessCriteria []string `json:"success_criteria"`
}

// Validate checks that the objective has a title.
func (o Objective) Validate() error {
	if strings.TrimSpace(o.Title) == "" {
		return errors.New("objective: title is required")
	}
	return nil
}

// Workflow describes a named agent workflow and its graph.
type Workflow struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Graph       workflow.Graph `json:"graph"`
}

// Validate checks the workflow name and graph.
func (w Workflow) Validate() error {
	var errs []error
	if strings.TrimSpace(w.Name) == "" {
		errs = append(errs, errors.New("workflow: name is required"))
	}
	if err := w.Graph.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("workflow: graph: %w", err))
	}
	return errors.Join(errs...)
}

// Claim states a proposition that knowledge sources can support.
type Claim struct {
	Text string `json:"text"`
}

// Validate checks that the claim contains nonblank text.
func (c Claim) Validate() error {
	if strings.TrimSpace(c.Text) == "" {
		return errors.New("claim: text is required")
	}
	return nil
}

// Source identifies source material and its content hash.
type Source struct {
	Title       string          `json:"title"`
	URI         string          `json:"uri"`
	ContentHash string          `json:"content_hash"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

// Validate checks that the source has a title or URI and object metadata.
func (s Source) Validate() error {
	var errs []error
	if strings.TrimSpace(s.Title) == "" && strings.TrimSpace(s.URI) == "" {
		errs = append(errs, errors.New("source: title or uri is required"))
	}
	if err := validateObject(s.Metadata); err != nil {
		errs = append(errs, fmt.Errorf("source: metadata must be a JSON object: %w", err))
	}
	return errors.Join(errs...)
}

// Agent describes an agent version and its model configuration.
type Agent struct {
	Name         string             `json:"name"`
	Description  string             `json:"description"`
	SystemPrompt string             `json:"system_prompt"`
	Model        string             `json:"model"`
	Config       domain.AgentConfig `json:"config"`
	OutputSchema json.RawMessage    `json:"output_schema,omitempty"`
	Notes        string             `json:"notes"`
}

// Validate checks the required agent fields, configuration and output schema.
func (a Agent) Validate() error {
	var errs []error
	if strings.TrimSpace(a.Name) == "" {
		errs = append(errs, errors.New("agent: name is required"))
	}
	if strings.TrimSpace(a.SystemPrompt) == "" {
		errs = append(errs, errors.New("agent: system prompt is required"))
	}
	if strings.TrimSpace(a.Model) == "" {
		errs = append(errs, errors.New("agent: model is required"))
	}
	if err := a.Config.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("agent: %w", err))
	}
	if len(bytes.TrimSpace(a.OutputSchema)) > 0 {
		if _, err := workflow.CompileSchema(a.OutputSchema); err != nil {
			errs = append(errs, fmt.Errorf("agent: output schema: %w", err))
		}
	}
	return errors.Join(errs...)
}

func validateObject(raw json.RawMessage) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	if raw[0] != '{' {
		return errors.New("not an object")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return fmt.Errorf("decode object: %w", err)
	}
	return nil
}
