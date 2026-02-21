package config

import "os"

type Config struct {
	DatabaseURL     string
	RedisURL        string
	OrchestratorURL string
	JaegerEndpoint  string
	Port            string
}

func Load() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	orchestratorURL := os.Getenv("ORCHESTRATOR_URL")
	if orchestratorURL == "" {
		orchestratorURL = "http://localhost:8082"
	}

	return &Config{
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		RedisURL:        os.Getenv("REDIS_URL"),
		OrchestratorURL: orchestratorURL,
		JaegerEndpoint:  os.Getenv("JAEGER_ENDPOINT"),
		Port:            port,
	}
}
