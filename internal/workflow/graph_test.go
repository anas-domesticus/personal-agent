package workflow

import (
	"slices"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		graph   string
		wantErr string
	}{
		{name: "single node", graph: `{"nodes":[{"id":"a","agent":"x"}],"edges":[]}`},
		{name: "diamond", graph: `{"nodes":[{"id":"a","agent":"x"},{"id":"b","agent":"x"},{"id":"c","agent":"y"},{"id":"d","agent":"y"}],
			"edges":[{"from":"a","to":"b"},{"from":"a","to":"c"},{"from":"b","to":"d"},{"from":"c","to":"d"}]}`},
		{name: "empty", graph: `{"nodes":[],"edges":[]}`, wantErr: "no nodes"},
		{name: "unknown field", graph: `{"nodes":[{"id":"a","agent":"x","model":"m"}],"edges":[]}`, wantErr: "unknown field"},
		{name: "bad id", graph: `{"nodes":[{"id":"A B","agent":"x"}],"edges":[]}`, wantErr: "must match"},
		{name: "reserved id", graph: `{"nodes":[{"id":"input","agent":"x"}],"edges":[]}`, wantErr: "reserved"},
		{name: "duplicate id", graph: `{"nodes":[{"id":"a","agent":"x"},{"id":"a","agent":"x"}],"edges":[]}`, wantErr: "duplicate id"},
		{name: "missing agent", graph: `{"nodes":[{"id":"a","agent":""}],"edges":[]}`, wantErr: "agent is required"},
		{name: "unknown edge node", graph: `{"nodes":[{"id":"a","agent":"x"}],"edges":[{"from":"a","to":"b"}]}`, wantErr: `unknown node "b"`},
		{name: "self loop", graph: `{"nodes":[{"id":"a","agent":"x"}],"edges":[{"from":"a","to":"a"}]}`, wantErr: "self-loop"},
		{name: "cycle", graph: `{"nodes":[{"id":"a","agent":"x"},{"id":"b","agent":"x"},{"id":"c","agent":"x"}],
			"edges":[{"from":"a","to":"b"},{"from":"b","to":"c"},{"from":"c","to":"b"}]}`, wantErr: "cycle through: b, c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.graph))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestOrderAndAgents(t *testing.T) {
	g, err := Parse([]byte(`{"nodes":[{"id":"d","agent":"y"},{"id":"a","agent":"x"},{"id":"b","agent":"x"}],
		"edges":[{"from":"a","to":"b"},{"from":"b","to":"d"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	order, err := g.Order()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(order, []string{"a", "b", "d"}) {
		t.Fatalf("order = %v", order)
	}
	if !slices.Equal(g.Agents(), []string{"y", "x"}) {
		t.Fatalf("agents = %v", g.Agents())
	}
}
