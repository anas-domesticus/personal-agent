package workflow_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		graph   string
		wantErr string // substring; empty means no error
	}{
		{name: "single node", graph: `{"nodes":[{"id":"a","agent":"x"}],"edges":[]}`},
		{name: "diamond", graph: `{"nodes":[{"id":"a","agent":"x"},{"id":"b","agent":"x"},{"id":"c","agent":"y"},{"id":"d","agent":"y"}],
			"edges":[{"from":"a","to":"b"},{"from":"a","to":"c"},{"from":"b","to":"d"},{"from":"c","to":"d"}]}`},
		{name: "empty", graph: `{"nodes":[],"edges":[]}`, wantErr: "no nodes"},
		{name: "not json", graph: `nope`, wantErr: "decode graph"},
		{name: "unknown field", graph: `{"nodes":[{"id":"a","agent":"x","model":"m"}],"edges":[]}`, wantErr: "unknown field"},
		{name: "bad id", graph: `{"nodes":[{"id":"A B","agent":"x"}],"edges":[]}`, wantErr: "must match"},
		{name: "reserved id", graph: `{"nodes":[{"id":"input","agent":"x"}],"edges":[]}`, wantErr: "reserved"},
		{name: "duplicate id", graph: `{"nodes":[{"id":"a","agent":"x"},{"id":"a","agent":"x"}],"edges":[]}`, wantErr: "duplicate id"},
		{name: "missing agent", graph: `{"nodes":[{"id":"a","agent":""}],"edges":[]}`, wantErr: "agent is required"},
		{name: "unknown edge node", graph: `{"nodes":[{"id":"a","agent":"x"}],"edges":[{"from":"a","to":"b"}]}`, wantErr: `unknown node "b"`},
		{name: "self loop", graph: `{"nodes":[{"id":"a","agent":"x"}],"edges":[{"from":"a","to":"a"}]}`, wantErr: "self-loop"},
		{name: "duplicate edge", graph: `{"nodes":[{"id":"a","agent":"x"},{"id":"b","agent":"x"}],
			"edges":[{"from":"a","to":"b"},{"from":"a","to":"b"}]}`, wantErr: "duplicate"},
		{name: "cycle", graph: `{"nodes":[{"id":"a","agent":"x"},{"id":"b","agent":"x"},{"id":"c","agent":"x"}],
			"edges":[{"from":"a","to":"b"},{"from":"b","to":"c"},{"from":"c","to":"b"}]}`, wantErr: "cycle through: b, c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := workflow.Parse([]byte(tt.graph))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestOrderAndAgents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		graph      string
		wantOrder  []string
		wantAgents []string
	}{
		{name: "chain declared out of order",
			graph: `{"nodes":[{"id":"d","agent":"y"},{"id":"a","agent":"x"},{"id":"b","agent":"x"}],
				"edges":[{"from":"a","to":"b"},{"from":"b","to":"d"}]}`,
			wantOrder: []string{"a", "b", "d"}, wantAgents: []string{"y", "x"}},
		{name: "independent nodes keep declaration order",
			graph:     `{"nodes":[{"id":"b","agent":"x"},{"id":"a","agent":"x"}],"edges":[]}`,
			wantOrder: []string{"b", "a"}, wantAgents: []string{"x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g, err := workflow.Parse([]byte(tt.graph))
			require.NoError(t, err)
			order, err := g.Order()
			require.NoError(t, err)
			assert.Equal(t, tt.wantOrder, order)
			assert.Equal(t, tt.wantAgents, g.Agents())
		})
	}
}
