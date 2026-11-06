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
	MCPConfigPath    string // where MCP was loaded from
	MCP              MCP
}

// Load reads the environment and the MCP file. The MCP file defaults to
// mcp.yaml in the working directory; a missing file means no MCP servers.
func Load() (Config, error) {
	c := Config{
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		OpenRouterAPIKey: os.Getenv("OPENROUTER_API_KEY"),
		EmbeddingModel:   getenv("EMBEDDING_MODEL", "openai/text-embedding-3-small"),
		AdminModel:       getenv("ADMIN_MODEL", "deepseek/deepseek-v4.1-flash"),
		MCPConfigPath:    getenv("MCP_CONFIG", "mcp.yaml"),
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required")
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
