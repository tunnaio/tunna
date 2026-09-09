package config_test

import (
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
