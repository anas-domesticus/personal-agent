// Package admin provides the tools for managing agents, workflows and runs.
// They are injected only into agents with the admin flag. None of them can
// change an agent's admin flag.
package admin

import (
	"encoding/json"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/catalog"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

type Deps struct {
	Catalog *catalog.Catalog
	// Assignable lists the tools that can be given to agents.
	Assignable func() []kit.Info
}

func Tools(d Deps) ([]tool.InvokableTool, error) {
	var all []tool.InvokableTool
	for _, group := range []func(Deps) ([]tool.InvokableTool, error){agentTools, workflowTools, runTools} {
		ts, err := group(d)
		if err != nil {
			return nil, err
		}
		all = append(all, ts...)
	}
	return all, nil
}

type nameIn struct {
	Name string `json:"name"`
}

type setVersionIn struct {
	Name    string `json:"name"`
	Version int32  `json:"version"`
}

func marshalOrNil(m map[string]any) json.RawMessage {
	if m == nil {
		return nil
	}
	b, _ := json.Marshal(m)
	return b
}
