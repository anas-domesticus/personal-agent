package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func lookup(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := env[k]; return v, ok }
}

func TestParseMCP(t *testing.T) {
	raw := []byte(`
servers:
  composio:
    transport: http
    url: https://mcp.example.com/${ACCOUNT}
    headers:
      x-api-key: ${COMPOSIO_API_KEY}
    tools: [GMAIL_FETCH_EMAILS]
  files:
    transport: stdio
    command: npx
    args: ["-y", "server-filesystem", "${HOME_DIR}"]
    disabled: true
  broken:
    transport: sse
    url: https://x/${NOT_SET}
    timeout: 5s
`)
	m, err := ParseMCP(raw, lookup(map[string]string{"ACCOUNT": "me", "COMPOSIO_API_KEY": "secret", "HOME_DIR": "/home"}))
	if err != nil {
		t.Fatal(err)
	}

	c := m.Servers["composio"]
	if c.URL != "https://mcp.example.com/me" || c.Headers["x-api-key"] != "secret" || len(c.Missing) != 0 {
		t.Fatalf("composio = %+v", c)
	}
	if c.Timeout != 30*time.Second {
		t.Fatalf("default timeout = %v", c.Timeout)
	}
	if f := m.Servers["files"]; f.Args[2] != "/home" || !f.Disabled {
		t.Fatalf("files = %+v", f)
	}
	if b := m.Servers["broken"]; !slices.Equal(b.Missing, []string{"NOT_SET"}) || b.Timeout != 5*time.Second {
		t.Fatalf("broken = %+v", b)
	}

	var names []string
	for _, s := range m.Enabled() {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"broken", "composio"}) {
		t.Fatalf("enabled = %v", names)
	}
}

func TestParseMCPRejects(t *testing.T) {
	tests := map[string]string{
		"unknown field":   "servers:\n  a:\n    transport: http\n    url: x\n    bogus: 1\n",
		"bad transport":   "servers:\n  a:\n    transport: carrier-pigeon\n",
		"http needs url":  "servers:\n  a:\n    transport: http\n",
		"stdio needs cmd": "servers:\n  a:\n    transport: stdio\n",
		"bad name":        "servers:\n  Bad Name:\n    transport: http\n    url: x\n",
		"stdio with url":  "servers:\n  a:\n    transport: stdio\n    command: x\n    url: y\n",
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseMCP([]byte(raw), lookup(nil)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestLoadMCP(t *testing.T) {
	dir := t.TempDir()

	m, err := LoadMCP(filepath.Join(dir, "missing.yaml"))
	if err != nil || len(m.Servers) != 0 {
		t.Fatalf("missing file: %+v, %v", m, err)
	}

	empty := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMCP(empty); err != nil {
		t.Fatalf("empty file: %v", err)
	}

	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("servers:\n  a:\n    transport: nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMCP(bad); err == nil || !strings.Contains(err.Error(), "bad.yaml") {
		t.Fatalf("bad file error should name the file: %v", err)
	}
}
