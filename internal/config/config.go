// Package config reads settings from the environment.
package config

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL      string
	MQTTURL          string
	OpenRouterAPIKey string
	EmbeddingModel   string // OpenRouter slug; must produce 1536 dimensions to match the schema
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		MQTTURL:          getenv("MQTT_URL", "tcp://localhost:1883"),
		OpenRouterAPIKey: os.Getenv("OPENROUTER_API_KEY"),
		EmbeddingModel:   getenv("EMBEDDING_MODEL", "openai/text-embedding-3-small"),
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
