// Package workflow defines the workflow graph format and checks it.
//
// A workflow is a DAG of nodes. Each node runs an agent (always the agent's
// current version at the moment the node runs). Data between nodes is
// structured JSON:
//
//   - A node's input is an object with the workflow's input under "input" and
//     each upstream node's output under that node's id.
//   - A node's output is a JSON object: one matching the agent's output schema
//     if it has one, otherwise {"text": "..."}.
//   - The workflow's output is an object of each sink node's output, keyed by
//     node id.
package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// InputKey is the key a node's input holds the workflow input under. No node
// may use it as its id.
const InputKey = "input"

type Graph struct {
	Nodes []Node `json:"nodes" jsonschema_description:"Nodes in the workflow. At least one."`
	Edges []Edge `json:"edges" jsonschema_description:"Directed edges. A node runs after all nodes with edges into it. No cycles."`
}

type Node struct {
	ID          string `json:"id" jsonschema_description:"Unique id within the workflow: lowercase letters, digits, '_' or '-'. Not 'input'."`
	Agent       string `json:"agent" jsonschema_description:"Name of the agent this node runs."`
	Instruction string `json:"instruction,omitempty" jsonschema_description:"Optional task for this node, given to the agent alongside its input."`
}

type Edge struct {
	From string `json:"from" jsonschema_description:"Upstream node id."`
	To   string `json:"to" jsonschema_description:"Downstream node id."`
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// Parse decodes a graph, rejecting unknown fields, and validates it.
func Parse(raw []byte) (Graph, error) {
	var g Graph
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&g); err != nil {
		return Graph{}, fmt.Errorf("decode graph: %w", err)
	}
	if err := g.Validate(); err != nil {
		return Graph{}, err
	}
	return g, nil
}

// Validate checks structure only. Whether the named agents exist is checked
// by the caller against the database.
func (g Graph) Validate() error {
	var errs []error
	if len(g.Nodes) == 0 {
		errs = append(errs, errors.New("graph has no nodes"))
	}

	nodes := make(map[string]bool, len(g.Nodes))
	for i, n := range g.Nodes {
		switch {
		case !idPattern.MatchString(n.ID):
			errs = append(errs, fmt.Errorf("node %d: id %q must match %s", i, n.ID, idPattern))
		case n.ID == InputKey:
			errs = append(errs, fmt.Errorf("node %d: id %q is reserved", i, InputKey))
		case nodes[n.ID]:
			errs = append(errs, fmt.Errorf("node %q: duplicate id", n.ID))
		}
		if strings.TrimSpace(n.Agent) == "" {
			errs = append(errs, fmt.Errorf("node %q: agent is required", n.ID))
		}
		nodes[n.ID] = true
	}

	seen := make(map[Edge]bool, len(g.Edges))
	for _, e := range g.Edges {
		if !nodes[e.From] {
			errs = append(errs, fmt.Errorf("edge %s->%s: unknown node %q", e.From, e.To, e.From))
		}
		if !nodes[e.To] {
			errs = append(errs, fmt.Errorf("edge %s->%s: unknown node %q", e.From, e.To, e.To))
		}
		if e.From == e.To {
			errs = append(errs, fmt.Errorf("edge %s->%s: self-loop", e.From, e.To))
		}
		if seen[e] {
			errs = append(errs, fmt.Errorf("edge %s->%s: duplicate", e.From, e.To))
		}
		seen[e] = true
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	if _, err := g.Order(); err != nil {
		return err
	}
	return nil
}

// Order returns node ids in an order where every node comes after its
// upstream nodes. It fails if the graph has a cycle.
func (g Graph) Order() ([]string, error) {
	indegree := make(map[string]int, len(g.Nodes))
	next := make(map[string][]string, len(g.Nodes))
	for _, n := range g.Nodes {
		indegree[n.ID] = 0
	}
	for _, e := range g.Edges {
		next[e.From] = append(next[e.From], e.To)
		indegree[e.To]++
	}

	var queue, order []string
	for _, n := range g.Nodes { // node order keeps the result deterministic
		if indegree[n.ID] == 0 {
			queue = append(queue, n.ID)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, id)
		for _, to := range next[id] {
			indegree[to]--
			if indegree[to] == 0 {
				queue = append(queue, to)
			}
		}
	}

	if len(order) != len(g.Nodes) {
		var stuck []string
		for _, n := range g.Nodes {
			if indegree[n.ID] > 0 {
				stuck = append(stuck, n.ID)
			}
		}
		return nil, fmt.Errorf("graph has a cycle through: %s", strings.Join(stuck, ", "))
	}
	return order, nil
}

// Agents returns the distinct agent names the graph uses.
func (g Graph) Agents() []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range g.Nodes {
		if !seen[n.Agent] {
			seen[n.Agent] = true
			out = append(out, n.Agent)
		}
	}
	return out
}
