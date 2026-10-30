package config_test

import (
	"path/filepath"
	"testing"

	"tally-mcp/internal/config"
)

func TestDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()

	if cfg.Server.ListenAddress != "0.0.0.0:8080" {
		t.Errorf("expected listen address 0.0.0.0:8080, got %s", cfg.Server.ListenAddress)
	}
	if cfg.Server.Transport != "sse" {
		t.Errorf("expected transport sse, got %s", cfg.Server.Transport)
	}
	if cfg.Tally.URL != "http://localhost:9000" {
		t.Errorf("expected tally url http://localhost:9000, got %s", cfg.Tally.URL)
	}
	if cfg.Auth.Enabled != false {
		t.Errorf("expected auth disabled by default, got %v", cfg.Auth.Enabled)
	}
	if cfg.Updater.CheckOnStartup != true {
		t.Errorf("expected check on startup true, got %v", cfg.Updater.CheckOnStartup)
	}
}

func TestConfigLoadAndEnvOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	err := config.WriteDefaultConfigFile(configPath)
	if err != nil {
		t.Fatalf("failed to write default config: %v", err)
	}

	// Test loading
	cfg, resolved, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if resolved != configPath {
		t.Errorf("expected resolved path %s, got %s", configPath, resolved)
	}

	// Test environment overrides
	t.Setenv("TALLY_URL", "http://192.168.1.100:9000")
	t.Setenv("TALLY_MCP_LISTEN", "127.0.0.1:9090")
	t.Setenv("TALLY_MCP_API_KEY", "super-secret-key")

	cfgWithEnv, _, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("failed to reload config: %v", err)
	}

	if cfgWithEnv.Tally.URL != "http://192.168.1.100:9000" {
		t.Errorf("env override failed for Tally URL: got %s", cfgWithEnv.Tally.URL)
	}
	if cfgWithEnv.Server.ListenAddress != "127.0.0.1:9090" {
		t.Errorf("env override failed for ListenAddress: got %s", cfgWithEnv.Server.ListenAddress)
	}
	if cfgWithEnv.Auth.APIKey != "super-secret-key" || !cfgWithEnv.Auth.Enabled {
		t.Errorf("env override failed for API Key")
	}
	_ = cfg
}
