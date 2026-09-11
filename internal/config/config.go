// Package config reads the server configuration from TUNNA_* environment
// variables, applies defaults, validates, and logs the effective values.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

const (
	envAddr         = "TUNNA_ADDR"
	envDataDir      = "TUNNA_DATA_DIR"
	envBootstrapKey = "TUNNA_BOOTSTRAP_KEY"
)

// Config is the effective configuration after defaults and validation.
type Config struct {
	Addr           string
	DataDir        string
	BootstrapKeyID string
	// Secret should never appear in any logs
	BootstrapKeySecret string
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

	bootstrapKey := os.Getenv(envBootstrapKey)
	if bootstrapKey != "" {
		key, secret, ok := strings.Cut(bootstrapKey, ":")
		if !ok || key == "" || secret == "" {
			return Config{}, fmt.Errorf("%s is invalid format, should be <id>:<secret>", envBootstrapKey)
		}
		cfg.BootstrapKeyID = key
		cfg.BootstrapKeySecret = secret
	}

	return cfg, nil
}

// LogValues records the effective configuration at startup. Container tools
// forward only the variables they are told to; this line shows what arrived.
func (c Config) LogValues(logger *slog.Logger) {
	logger.Info("config", "addr", c.Addr, "data_dir", c.DataDir, "bootstrap_key_id", c.BootstrapKeyID)
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}

	return fallback
}
