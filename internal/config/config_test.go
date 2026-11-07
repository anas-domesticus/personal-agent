package config_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/config"
)

// TestLoad sets process environment variables, so neither it nor its cases
// can run in parallel.
func TestLoad(t *testing.T) {
	missingMCP := filepath.Join(t.TempDir(), "missing.yaml")

	tests := []struct {
		name    string
		env     map[string]string
		want    config.Config
		wantErr string // substring; empty means no error
	}{
		{
			name: "defaults",
			env:  map[string]string{"DATABASE_URL": "postgres://db"},
			want: config.Config{
				DatabaseURL:    "postgres://db",
				EmbeddingModel: "openai/text-embedding-3-small",
				SearchModel:    "deepseek/deepseek-v4.1-flash",
				Workspace:      config.WorkspaceLocal,
				WorkspaceDir:   "workspace",
				MCPConfigPath:  missingMCP,
				ListenAddr:     "127.0.0.1:7433",
			},
		},
		{
			name: "overrides",
			env: map[string]string{
				"DATABASE_URL": "postgres://db", "OPENROUTER_API_KEY": "key",
				"EMBEDDING_MODEL": "e/m", "SEARCH_MODEL": "s/m",
				"WORKSPACE": config.WorkspaceSandbox, "WORKSPACE_DIR": "/ws",
				"MCP_LISTEN": "127.0.0.1:9000",
			},
			want: config.Config{
				DatabaseURL: "postgres://db", OpenRouterAPIKey: "key",
				EmbeddingModel: "e/m", SearchModel: "s/m",
				Workspace: config.WorkspaceSandbox, WorkspaceDir: "/ws", MCPConfigPath: missingMCP,
				ListenAddr: "127.0.0.1:9000",
			},
		},
		{name: "database url is required", env: map[string]string{}, wantErr: "DATABASE_URL is required"},
		{name: "unknown workspace", env: map[string]string{"DATABASE_URL": "postgres://db", "WORKSPACE": "cloud"},
			wantErr: "WORKSPACE must be"},
		{name: "unauthenticated server must be local", env: map[string]string{"DATABASE_URL": "postgres://db", "MCP_LISTEN": ":9000"},
			wantErr: "MCP_LISTEN must use a loopback address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{
				"DATABASE_URL", "OPENROUTER_API_KEY", "EMBEDDING_MODEL",
				"SEARCH_MODEL", "WORKSPACE", "WORKSPACE_DIR", "MCP_LISTEN",
			} {
				t.Setenv(k, tt.env[k])
			}
			t.Setenv("MCP_CONFIG", missingMCP)

			got, err := config.Load()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
