package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/config"
)

func lookup(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := env[k]; return v, ok }
}

func TestParseMCP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		raw         string
		env         map[string]string
		want        map[string]config.MCPServer
		wantEnabled []string
		wantErr     string // substring; empty means no error
	}{
		{
			name: "expands variables and applies defaults",
			raw: `
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
    env:
      ROOT: ${HOME_DIR}
    disabled: true
  broken:
    transport: sse
    url: https://x/${NOT_SET}
    timeout: 5s
`,
			env: map[string]string{"ACCOUNT": "me", "COMPOSIO_API_KEY": "secret", "HOME_DIR": "/home"},
			want: map[string]config.MCPServer{
				"composio": {
					Name: "composio", Transport: config.TransportHTTP, URL: "https://mcp.example.com/me",
					Headers: map[string]string{"x-api-key": "secret"}, Tools: []string{"GMAIL_FETCH_EMAILS"},
					Timeout: 30 * time.Second,
				},
				"files": {
					Name: "files", Transport: config.TransportStdio, Command: "npx",
					Args: []string{"-y", "server-filesystem", "/home"}, Env: map[string]string{"ROOT": "/home"},
					Disabled: true, Timeout: 30 * time.Second,
				},
				"broken": {
					Name: "broken", Transport: config.TransportSSE, URL: "https://x/",
					Timeout: 5 * time.Second, Missing: []string{"NOT_SET"},
				},
			},
			wantEnabled: []string{"broken", "composio"},
		},
		{name: "empty", raw: "", want: nil},
		{name: "not yaml", raw: "servers: [", wantErr: "decode mcp config"},
		{name: "unknown field", raw: "servers:\n  a:\n    transport: http\n    url: x\n    bogus: 1\n", wantErr: "bogus"},
		{name: "bad transport", raw: "servers:\n  a:\n    transport: carrier-pigeon\n", wantErr: "transport must be http, sse or stdio"},
		{name: "http needs url", raw: "servers:\n  a:\n    transport: http\n", wantErr: "url is required"},
		{name: "http with command", raw: "servers:\n  a:\n    transport: http\n    url: x\n    command: y\n", wantErr: "only for stdio"},
		{name: "stdio needs command", raw: "servers:\n  a:\n    transport: stdio\n", wantErr: "command is required"},
		{name: "bad name", raw: "servers:\n  Bad Name:\n    transport: http\n    url: x\n", wantErr: "name must match"},
		{name: "stdio with url", raw: "servers:\n  a:\n    transport: stdio\n    command: x\n    url: y\n", wantErr: "not used by stdio"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := config.ParseMCP([]byte(tt.raw), lookup(tt.env))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, m.Servers)
			var enabled []string
			for _, s := range m.Enabled() {
				enabled = append(enabled, s.Name)
			}
			assert.Equal(t, tt.wantEnabled, enabled)
		})
	}
}

func TestLoadMCP(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "empty.yaml"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte("servers:\n  a:\n    transport: nope\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "good.yaml"), []byte("servers:\n  a:\n    transport: http\n    url: x\n"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "dir.yaml"), 0o755))

	tests := []struct {
		name        string
		file        string
		wantServers []string
		wantErr     string // substring; empty means no error
	}{
		{name: "missing file means no servers", file: "missing.yaml"},
		{name: "empty file", file: "empty.yaml"},
		{name: "valid file", file: "good.yaml", wantServers: []string{"a"}},
		{name: "invalid file names the file", file: "bad.yaml", wantErr: "bad.yaml"},
		{name: "unreadable file", file: "dir.yaml", wantErr: "read "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := config.LoadMCP(filepath.Join(dir, tt.file))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			var names []string
			for _, s := range m.Enabled() {
				names = append(names, s.Name)
			}
			assert.Equal(t, tt.wantServers, names)
		})
	}
}
