package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration.
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Tally   TallyConfig   `yaml:"tally"`
	Auth    AuthConfig    `yaml:"auth"`
	Updater UpdaterConfig `yaml:"updater"`
	Log     LogConfig     `yaml:"log"`
}

// ServerConfig defines the MCP server settings.
type ServerConfig struct {
	// ListenAddress is the network address and port for SSE transport (e.g., "0.0.0.0:8080").
	ListenAddress string `yaml:"listen_address"`
	// Transport can be "sse" (default, over HTTP for local network) or "stdio" (stdin/stdout).
	Transport string `yaml:"transport"`
}

// TallyConfig defines connection details to Tally ERP 9 / TallyPrime.
type TallyConfig struct {
	// URL is the HTTP endpoint of Tally (default "http://localhost:9000").
	URL string `yaml:"url"`
	// TimeoutSeconds is the HTTP timeout for Tally requests.
	TimeoutSeconds int `yaml:"timeout_seconds"`
	// DefaultCompany optionally targets a specific company name if multiple companies are loaded.
	DefaultCompany string `yaml:"default_company"`
	// MaxQueueSize buffers incoming queries to prevent overloading Tally (default: 100).
	MaxQueueSize int `yaml:"max_queue_size"`
	// ThrottleMs is a small cooldown period between queries in milliseconds (default: 15).
	ThrottleMs int `yaml:"throttle_ms"`
}

// AuthConfig defines API key / Bearer token security for local network access.
type AuthConfig struct {
	// Enabled determines if incoming MCP requests require an API key / Bearer token.
	Enabled bool `yaml:"enabled"`
	// APIKey is the secret token required when Enabled is true.
	APIKey string `yaml:"api_key"`
}

// UpdaterConfig defines self-update parameters.
type UpdaterConfig struct {
	// GitHubRepo in "owner/repo" format (e.g. "myorg/tally-mcp").
	GitHubRepo string `yaml:"github_repo"`
	// CheckOnStartup checks GitHub Releases for newer versions on server start.
	CheckOnStartup bool `yaml:"check_on_startup"`
	// AutoApply automatically downloads and applies updates on startup if found.
	AutoApply bool `yaml:"auto_apply"`
}

// LogConfig defines logging verbosity.
type LogConfig struct {
	Level string `yaml:"level"` // "debug", "info", "warn", "error"
}

// DefaultConfig returns default configuration settings.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			ListenAddress: "0.0.0.0:8080",
			Transport:     "sse",
		},
		Tally: TallyConfig{
			URL:            "http://localhost:9000",
			TimeoutSeconds: 30,
			DefaultCompany: "",
			MaxQueueSize:   100,
			ThrottleMs:     15,
		},
		Auth: AuthConfig{
			Enabled: false,
			APIKey:  "",
		},
		Updater: UpdaterConfig{
			GitHubRepo:     "gops/TallyMCP",
			CheckOnStartup: true,
			AutoApply:      false,
		},
		Log: LogConfig{
			Level: "info",
		},
	}
}

// Load loads the configuration from a file path, or falls back to defaults.
// It also checks environment variables for overrides.
func Load(configPath string) (*Config, string, error) {
	cfg := DefaultConfig()
	resolvedPath := ""

	// Determine configuration candidate paths if not explicitly given
	candidates := []string{}
	if configPath != "" {
		candidates = append(candidates, configPath)
	} else {
		// Look in current working directory
		candidates = append(candidates, "config.yaml", "config.yml")

		// Look next to executable
		if exePath, err := os.Executable(); err == nil {
			exeDir := filepath.Dir(exePath)
			candidates = append(candidates, filepath.Join(exeDir, "config.yaml"))
		}
	}

	var found bool
	for _, candidate := range candidates {
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			data, err := os.ReadFile(candidate)
			if err != nil {
				return nil, "", fmt.Errorf("reading config file %s: %w", candidate, err)
			}
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, "", fmt.Errorf("parsing YAML config %s: %w", candidate, err)
			}
			resolvedPath = candidate
			found = true
			break
		}
	}

	if !found && configPath != "" {
		return nil, "", fmt.Errorf("config file not found at %s", configPath)
	}

	// Environment variable overrides
	applyEnvOverrides(cfg)

	return cfg, resolvedPath, nil
}

func applyEnvOverrides(cfg *Config) {
	if val := os.Getenv("TALLY_URL"); val != "" {
		cfg.Tally.URL = strings.TrimRight(val, "/")
	}
	if val := os.Getenv("TALLY_MCP_LISTEN"); val != "" {
		cfg.Server.ListenAddress = val
	}
	if val := os.Getenv("TALLY_MCP_TRANSPORT"); val != "" {
		cfg.Server.Transport = val
	}
	if val := os.Getenv("TALLY_MCP_API_KEY"); val != "" {
		cfg.Auth.APIKey = val
		cfg.Auth.Enabled = true
	}
	if val := os.Getenv("TALLY_MCP_GITHUB_REPO"); val != "" {
		cfg.Updater.GitHubRepo = val
	}
	if val := os.Getenv("TALLY_MAX_QUEUE_SIZE"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			cfg.Tally.MaxQueueSize = n
		}
	}
	if val := os.Getenv("TALLY_THROTTLE_MS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n >= 0 {
			cfg.Tally.ThrottleMs = n
		}
	}
}

// WriteDefaultConfigFile writes a fully commented default config file to path.
func WriteDefaultConfigFile(path string) error {
	content := `# Tally ERP & TallyPrime MCP Server Configuration
# MIT Licensed Open Source Project

server:
  # Network address to listen on for MCP clients (0.0.0.0 allows any machine on LAN)
  listen_address: "0.0.0.0:8080"
  # Transport mode: "sse" (HTTP mode exposing /mcp and /sse for LAN) or "stdio" (Standard I/O)
  transport: "sse"

tally:
  # HTTP address of Tally ERP 9 or TallyPrime
  # In Tally, ensure "Client/Server configuration" has Server enabled on port 9000
  url: "http://localhost:9000"
  # Request timeout in seconds
  timeout_seconds: 30
  # Specific company name (leave blank to use currently active company in Tally)
  default_company: ""
  # Maximum number of incoming queries to buffer when Tally is busy (prevents overload)
  max_queue_size: 100
  # Throttle cooldown in milliseconds between queries (ensures no parallel queries)
  throttle_ms: 15

auth:
  # Set to true to require authentication from LAN clients
  enabled: false
  # Secret key to authenticate. Clients pass: Authorization: Bearer <key> or ?token=<key>
  api_key: ""

updater:
  # GitHub repository coordinates to check for new releases (owner/repo)
  github_repo: "gops/TallyMCP"
  # Check for new version on application startup
  check_on_startup: true
  # Automatically apply updates if a new version is detected
  auto_apply: false

log:
  # Logging level: debug, info, warn, error
  level: "info"
`
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0644)
}
