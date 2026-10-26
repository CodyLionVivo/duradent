// Package config loads process configuration from environment variables.
// Secrets arrive the same way: in AWS they are injected from Secrets Manager
// into the ECS task environment, so the code never talks to Secrets Manager.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Env             string // local | staging | prod
	HTTPAddr        string
	MySQLDSN        string
	LogLevel        string
	ShutdownTimeout time.Duration
}

// Load reads configuration from the environment, applying defaults suitable
// for local development. It fails if a required value is missing outside local.
func Load() (Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Env:      valueOr(getenv("APP_ENV"), "local"),
		HTTPAddr: valueOr(getenv("HTTP_ADDR"), ":8080"),
		MySQLDSN: getenv("MYSQL_DSN"),
		LogLevel: valueOr(getenv("LOG_LEVEL"), "info"),
	}

	timeout, err := durationOr(getenv("SHUTDOWN_TIMEOUT"), 15*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT: %w", err)
	}
	cfg.ShutdownTimeout = timeout

	if cfg.MySQLDSN == "" {
		if cfg.Env != "local" {
			return Config{}, errors.New("MYSQL_DSN is required")
		}
		cfg.MySQLDSN = "duradent:duradent@tcp(localhost:3306)/duradent?parseTime=true&multiStatements=true"
	}
	return cfg, nil
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func durationOr(v string, fallback time.Duration) (time.Duration, error) {
	if v == "" {
		return fallback, nil
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d, nil
	}
	secs, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", v)
	}
	return time.Duration(secs) * time.Second, nil
}
