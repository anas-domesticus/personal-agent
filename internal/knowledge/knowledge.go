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

// SupportedByKind identifies a claim-to-source support relationship.
const SupportedByKind = "supported_by"

// SupportedBy is the empty payload of a claim-to-source support relationship.
type SupportedBy struct{}

// Details contains the common descriptive fields for knowledge nodes.
type Details struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// NodeText returns the title and description used to represent a knowledge node.
func (d Details) NodeText() (title, description string) {
	return d.Title, d.Description
}

// Validate checks that the details have a nonblank title.
func (d Details) Validate() error {
	if strings.TrimSpace(d.Title) == "" {
		return errors.New("title is required")
	}
	return nil
}

// Objective describes a desired outcome and its success criteria.
type Objective struct {
	Details
	SuccessCriteria []string `json:"success_criteria"`
}

// Validate checks that the objective has a title.
func (o Objective) Validate() error {
	if err := o.Details.Validate(); err != nil {
		return fmt.Errorf("objective: %w", err)
	}
	return nil
}

// Workflow describes a named agent workflow and its graph.
type Workflow struct {
	Details
	Graph workflow.Graph `json:"graph"`
}

// Validate checks the workflow title and graph.
func (w Workflow) Validate() error {
	var errs []error
	if err := w.Details.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("workflow: %w", err))
	}
	if err := w.Graph.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("workflow: graph: %w", err))
	}
	return errors.Join(errs...)
}

// Claim states a proposition that knowledge sources can support.
type Claim struct {
	Details
}

// Validate checks that the claim has a title.
func (c Claim) Validate() error {
	if err := c.Details.Validate(); err != nil {
		return fmt.Errorf("claim: %w", err)
	}
	return nil
}

// Source identifies source material and its content hash.
type Source struct {
	Details
	URI         string          `json:"uri"`
	ContentHash string          `json:"content_hash"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

// Validate checks that the source has a title and object metadata.
func (s Source) Validate() error {
	var errs []error
	if err := s.Details.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("source: %w", err))
	}
	if err := validateObject(s.Metadata); err != nil {
		errs = append(errs, fmt.Errorf("source: metadata must be a JSON object: %w", err))
	}
	return errors.Join(errs...)
}

// Agent describes an agent version and its model configuration.
type Agent struct {
	Details
	SystemPrompt string             `json:"system_prompt"`
	Model        string             `json:"model"`
	Config       domain.AgentConfig `json:"config"`
	OutputSchema json.RawMessage    `json:"output_schema,omitempty"`
	Notes        string             `json:"notes"`
}

// Validate checks the required agent fields, configuration and output schema.
func (a Agent) Validate() error {
	var errs []error
	if err := a.Details.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("agent: %w", err))
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
