package config

import "os"

type Config struct {
	DatabaseURL          string
	RedisURL             string
	OrchestratorGRPCAddr string
	JaegerEndpoint       string
	Port                 string
}

func Load() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	orchestratorGRPCAddr := os.Getenv("ORCHESTRATOR_GRPC_ADDR")
	if orchestratorGRPCAddr == "" {
		orchestratorGRPCAddr = "localhost:50051"
	}

	return &Config{
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		RedisURL:             os.Getenv("REDIS_URL"),
		OrchestratorGRPCAddr: orchestratorGRPCAddr,
		JaegerEndpoint:       os.Getenv("JAEGER_ENDPOINT"),
		Port:                 port,
	}
}
