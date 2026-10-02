package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")

	content := `
# Comment line
SPECTER_TEST_KEY=secret_value
export SPECTER_EXPORTED="quoted_val"
SPECTER_SINGLE_QUOTED='single'
EMPTY_LINE=

`
	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed writing temp .env: %v", err)
	}

	if err := LoadDotEnv(envPath); err != nil {
		t.Fatalf("LoadDotEnv failed: %v", err)
	}

	if val := os.Getenv("SPECTER_TEST_KEY"); val != "secret_value" {
		t.Errorf("expected secret_value, got %s", val)
	}
	if val := os.Getenv("SPECTER_EXPORTED"); val != "quoted_val" {
		t.Errorf("expected quoted_val, got %s", val)
	}
	if val := os.Getenv("SPECTER_SINGLE_QUOTED"); val != "single" {
		t.Errorf("expected single, got %s", val)
	}
}
