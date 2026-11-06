// Package config loads all runtime configuration: settings from the
// environment, and MCP servers from a YAML file.
package config

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL      string
	OpenRouterAPIKey string
	EmbeddingModel   string // OpenRouter slug; must produce 1536 dimensions to match the schema
	AdminModel       string // OpenRouter slug for the admin agent's first version
	SearchModel      string // OpenRouter slug that runs web searches
	Workspace        string // where file and shell tools act: "local" or "sandbox"
	WorkspaceDir     string // root of the local workspace
	MCPConfigPath    string // where MCP was loaded from
	MCP              MCP
	ListenAddr       string // where `agent serve` listens
	Token            string // the bearer token clients must send; see cmd/agent
}

const (
	WorkspaceLocal   = "local"
	WorkspaceSandbox = "sandbox"
)

// Load reads the environment and the MCP file. The MCP file defaults to
// mcp.yaml in the working directory; a missing file means no MCP servers.
func Load() (Config, error) {
	c := Config{
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		OpenRouterAPIKey: os.Getenv("OPENROUTER_API_KEY"),
		EmbeddingModel:   getenv("EMBEDDING_MODEL", "openai/text-embedding-3-small"),
		AdminModel:       getenv("ADMIN_MODEL", "deepseek/deepseek-v4.1-flash"),
		SearchModel:      getenv("SEARCH_MODEL", "deepseek/deepseek-v4.1-flash"),
		Workspace:        getenv("WORKSPACE", WorkspaceLocal),
		WorkspaceDir:     getenv("WORKSPACE_DIR", "workspace"),
		MCPConfigPath:    getenv("MCP_CONFIG", "mcp.yaml"),
		ListenAddr:       getenv("AGENT_LISTEN", "127.0.0.1:7433"),
		Token:            os.Getenv("AGENT_TOKEN"),
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required")
	}
	if c.Workspace != WorkspaceLocal && c.Workspace != WorkspaceSandbox {
		return c, fmt.Errorf("WORKSPACE must be %q or %q", WorkspaceLocal, WorkspaceSandbox)
	}
	mcp, err := LoadMCP(c.MCPConfigPath)
	if err != nil {
		return c, err
	}
	c.MCP = mcp
	return c, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
