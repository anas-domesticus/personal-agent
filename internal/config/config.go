// Package config reads settings from the environment.
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
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		OpenRouterAPIKey: os.Getenv("OPENROUTER_API_KEY"),
		EmbeddingModel:   getenv("EMBEDDING_MODEL", "openai/text-embedding-3-small"),
		AdminModel:       getenv("ADMIN_MODEL", "deepseek/deepseek-v4.1-flash"),
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required")
	}
	return c, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
