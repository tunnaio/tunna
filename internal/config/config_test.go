package config_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/tunnaio/tunna/internal/config"
)

// The variable names are spelled out here rather than taken from the package
// so that a rename in the package is caught: users type these strings.

func TestLoadAppliesDefaults(t *testing.T) {
	t.Setenv("TUNNA_ADDR", "")
	t.Setenv("TUNNA_DATA_DIR", "/var/lib/tunna")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DataDir != "/var/lib/tunna" {
		t.Errorf("DataDir = %q, want the value that was set", cfg.DataDir)
	}
	if cfg.Addr == "" {
		t.Errorf("Addr is empty; an unset TUNNA_ADDR should fall back to a default")
	}
}

func TestLoadHonoursOverrides(t *testing.T) {
	t.Setenv("TUNNA_ADDR", "127.0.0.1:9999")
	t.Setenv("TUNNA_DATA_DIR", "./data")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9999" {
		t.Errorf("Addr = %q, want the override", cfg.Addr)
	}
	if cfg.DataDir != "./data" {
		t.Errorf("DataDir = %q, want the override", cfg.DataDir)
	}
}

func TestLoadRequiresDataDir(t *testing.T) {
	t.Setenv("TUNNA_ADDR", ":8000")
	t.Setenv("TUNNA_DATA_DIR", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load succeeded without TUNNA_DATA_DIR; it must fail")
	}
	if !strings.Contains(err.Error(), "TUNNA_DATA_DIR") {
		t.Errorf("error %q does not name the missing variable", err)
	}
}

func TestLoadBootstrapKeyAbsentMeansNone(t *testing.T) {
	t.Setenv("TUNNA_DATA_DIR", "./data")
	t.Setenv("TUNNA_BOOTSTRAP_KEY", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BootstrapKeyID != "" || cfg.BootstrapKeySecret != "" {
		t.Errorf("bootstrap key = %q/%q, want empty when the variable is unset", cfg.BootstrapKeyID, cfg.BootstrapKeySecret)
	}
}

func TestLoadBootstrapKeySplitsOnFirstColon(t *testing.T) {
	t.Setenv("TUNNA_DATA_DIR", "./data")
	t.Setenv("TUNNA_BOOTSTRAP_KEY", "tk_admin:s3cr3t:with:colons")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BootstrapKeyID != "tk_admin" {
		t.Errorf("BootstrapKeyID = %q, want tk_admin", cfg.BootstrapKeyID)
	}
	if cfg.BootstrapKeySecret != "s3cr3t:with:colons" {
		t.Errorf("BootstrapKeySecret = %q, want everything after the first colon", cfg.BootstrapKeySecret)
	}
}

func TestLoadBootstrapKeyMalformed(t *testing.T) {
	for _, bad := range []string{"nocolon", ":secret-only", "id-only:", ":"} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv("TUNNA_DATA_DIR", "./data")
			t.Setenv("TUNNA_BOOTSTRAP_KEY", bad)

			_, err := config.Load()
			if err == nil {
				t.Fatalf("Load accepted TUNNA_BOOTSTRAP_KEY=%q", bad)
			}
			if !strings.Contains(err.Error(), "TUNNA_BOOTSTRAP_KEY") {
				t.Errorf("error %q does not name the variable", err)
			}
		})
	}
}

func TestLogValuesRedactsBootstrapSecret(t *testing.T) {
	cfg := config.Config{
		Addr:               ":8000",
		DataDir:            "./data",
		BootstrapKeyID:     "tk_admin",
		BootstrapKeySecret: "hunter2-never-logged",
	}

	var buf bytes.Buffer
	cfg.LogValues(slog.New(slog.NewTextHandler(&buf, nil)))
	out := buf.String()

	if strings.Contains(out, "hunter2-never-logged") {
		t.Fatalf("startup log leaks the bootstrap secret:\n%s", out)
	}
	if !strings.Contains(out, "tk_admin") {
		t.Errorf("startup log should name the bootstrap key id:\n%s", out)
	}
}
