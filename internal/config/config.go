// Package config reads the server configuration from TUNNA_* environment
// variables, applies defaults, validates, and logs the effective values.
package config

import (
	"fmt"
	"log/slog"
	"os"
)

const (
	envAddr    = "TUNNA_ADDR"
	envDataDir = "TUNNA_DATA_DIR"
)

// Config is the effective configuration after defaults and validation.
type Config struct {
	Addr    string
	DataDir string
}

// Load reads the environment. An unset or empty variable takes its default;
// TUNNA_DATA_DIR has none and is required.
func Load() (Config, error) {
	cfg := Config{
		Addr:    envOr(envAddr, ":8000"),
		DataDir: os.Getenv(envDataDir),
	}

	if cfg.DataDir == "" {
		return Config{}, fmt.Errorf("%s is required", envDataDir)
	}

	return cfg, nil
}

// LogValues records the effective configuration at startup. Container tools
// forward only the variables they are told to; this line shows what arrived.
func (c Config) LogValues(logger *slog.Logger) {
	logger.Info("config", "addr", c.Addr, "data_dir", c.DataDir)
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}

	return fallback
}
