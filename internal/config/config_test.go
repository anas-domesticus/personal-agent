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
				AdminModel:     "deepseek/deepseek-v4.1-flash",
				SearchModel:    "deepseek/deepseek-v4.1-flash",
				Workspace:      config.WorkspaceLocal,
				WorkspaceDir:   "workspace",
				MCPConfigPath:  missingMCP,
			},
		},
		{
			name: "overrides",
			env: map[string]string{
				"DATABASE_URL": "postgres://db", "OPENROUTER_API_KEY": "key",
				"EMBEDDING_MODEL": "e/m", "ADMIN_MODEL": "a/m", "SEARCH_MODEL": "s/m",
				"WORKSPACE": config.WorkspaceSandbox, "WORKSPACE_DIR": "/ws",
			},
			want: config.Config{
				DatabaseURL: "postgres://db", OpenRouterAPIKey: "key",
				EmbeddingModel: "e/m", AdminModel: "a/m", SearchModel: "s/m",
				Workspace: config.WorkspaceSandbox, WorkspaceDir: "/ws", MCPConfigPath: missingMCP,
			},
		},
		{name: "database url is required", env: map[string]string{}, wantErr: "DATABASE_URL is required"},
		{name: "unknown workspace", env: map[string]string{"DATABASE_URL": "postgres://db", "WORKSPACE": "cloud"},
			wantErr: "WORKSPACE must be"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{
				"DATABASE_URL", "OPENROUTER_API_KEY", "EMBEDDING_MODEL", "ADMIN_MODEL",
				"SEARCH_MODEL", "WORKSPACE", "WORKSPACE_DIR",
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
