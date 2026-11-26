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
	Relay   RelayConfig   `yaml:"relay"`
	Updater UpdaterConfig `yaml:"updater"`
	Log     LogConfig     `yaml:"log"`
}

// RelayConfig defines outbound relay settings for connecting to 1erp.work.
type RelayConfig struct {
	// Enabled controls whether TallyMCP connects to the 1erp.work relay when logged in.
	Enabled bool `yaml:"enabled"`
	// ServerURL is the base URL of the 1erp.work server (must be https:// for non-localhost).
	ServerURL string `yaml:"server_url"`
	// CredentialsFile optionally overrides the path where 1erp.work OAuth credentials are stored.
	CredentialsFile string `yaml:"credentials_file"`
	// AllowWrites allows mutating MCP operations (e.g. tally_create_ledger) over the 1erp.work relay.
	// Defaults to false for safety: relayed requests run in read-only mode unless explicitly enabled.
	AllowWrites bool `yaml:"allow_writes"`
}

// ServerConfig defines the MCP server settings.
type ServerConfig struct {
	// ListenAddress is the network address and port for SSE transport (e.g., "0.0.0.0:8080").
	ListenAddress string `yaml:"listen_address"`
	// Transport can be "sse" (default, over HTTP for local network) or "stdio" (stdin/stdout).
	Transport string `yaml:"transport"`
	// PayloadFormat sets the serialization format for tool responses ("toon" by default).
	PayloadFormat string `yaml:"payload_format"`
	// ReadOnly blocks mutating MCP operations (e.g. tally_create_ledger, tally_create_voucher).
	ReadOnly bool `yaml:"read_only"`
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
	// DisableThrottle explicitly disables inter-query throttling (e.g. when throttle_ms is 0).
	DisableThrottle bool `yaml:"disable_throttle"`
	// CacheTTLSeconds is the TTL for master data in-memory cache in seconds (default: 120).
	CacheTTLSeconds int `yaml:"cache_ttl_seconds"`
	// DisableCache explicitly disables the in-memory master data cache (e.g. when cache_ttl_seconds is 0).
	DisableCache bool `yaml:"disable_cache"`
	// ExePath is the optional file path to tally.exe / TallyPrime executable.
	ExePath string `yaml:"exe_path"`
	// AutoStart automatically attempts to start Tally if it is not running when needed.
	AutoStart bool `yaml:"auto_start"`
}

// AuthConfig defines API key / Bearer token security for local network access.
type AuthConfig struct {
	// Enabled determines if incoming MCP requests require an API key / Bearer token.
	Enabled bool `yaml:"enabled"`
	// APIKey is the secret token required when Enabled is true.
	APIKey string `yaml:"api_key"`
	// DisableQueryToken disables ?token= / ?api_key= URL query parameter auth so tokens are only accepted via headers.
	DisableQueryToken bool `yaml:"disable_query_token"`
}

// UpdaterConfig defines self-update parameters.
type UpdaterConfig struct {
	// GitHubRepo in "owner/repo" format (e.g. "myorg/tally-mcp").
	GitHubRepo string `yaml:"github_repo"`
	// CheckOnStartup checks GitHub Releases for newer versions on server start.
	CheckOnStartup bool `yaml:"check_on_startup"`
	// CheckIntervalSeconds is how often (in seconds) to poll GitHub Releases for updates (default: 300, 0 disables periodic polling).
	CheckIntervalSeconds int `yaml:"check_interval_seconds"`
	// AutoApply automatically downloads, installs updates, and restarts the server when a new version is found (default: true).
	AutoApply bool `yaml:"auto_apply"`
	// ExposeMCPTools exposes system_check_update and system_apply_update in MCP tools/list (default: false to save schema tokens).
	ExposeMCPTools bool `yaml:"expose_mcp_tools"`
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
			PayloadFormat: "toon",
		},
		Tally: TallyConfig{
			URL:             "http://localhost:9000",
			TimeoutSeconds:  30,
			DefaultCompany:  "",
			MaxQueueSize:    100,
			ThrottleMs:      15,
			CacheTTLSeconds: 120,
			ExePath:         "",
			AutoStart:       false,
		},
		Auth: AuthConfig{
			Enabled: false,
			APIKey:  "",
		},
		Relay: RelayConfig{
			Enabled:     true,
			ServerURL:   "https://1erp.work",
			AllowWrites: false,
		},
		Updater: UpdaterConfig{
			GitHubRepo:           "gops/TallyMCP",
			CheckOnStartup:       true,
			CheckIntervalSeconds: 300,
			AutoApply:            true,
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
			// Preserve explicit 0 values from YAML instead of falling back to defaults.
			if cfg.Tally.ThrottleMs == 0 {
				cfg.Tally.DisableThrottle = true
			}
			if cfg.Tally.CacheTTLSeconds == 0 {
				cfg.Tally.DisableCache = true
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
	if val := os.Getenv("TALLY_MCP_PAYLOAD_FORMAT"); val != "" {
		cfg.Server.PayloadFormat = strings.ToLower(strings.TrimSpace(val))
	}
	if val := os.Getenv("TALLY_MCP_READ_ONLY"); val != "" {
		valLower := strings.ToLower(strings.TrimSpace(val))
		cfg.Server.ReadOnly = valLower == "true" || valLower == "1" || valLower == "yes" || valLower == "on"
	}
	if val := os.Getenv("TALLY_MCP_API_KEY"); val != "" {
		cfg.Auth.APIKey = val
		cfg.Auth.Enabled = true
	}
	if val := os.Getenv("TALLY_MCP_DISABLE_QUERY_TOKEN"); val != "" {
		valLower := strings.ToLower(strings.TrimSpace(val))
		cfg.Auth.DisableQueryToken = valLower == "true" || valLower == "1" || valLower == "yes" || valLower == "on"
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
			cfg.Tally.DisableThrottle = (n == 0)
		}
	}
	if val := os.Getenv("TALLY_CACHE_TTL_SECONDS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n >= 0 {
			cfg.Tally.CacheTTLSeconds = n
			cfg.Tally.DisableCache = (n == 0)
		}
	}
	if val := os.Getenv("TALLY_EXE_PATH"); val != "" {
		cfg.Tally.ExePath = val
	}
	if val := os.Getenv("TALLY_AUTO_START"); val != "" {
		valLower := strings.ToLower(strings.TrimSpace(val))
		cfg.Tally.AutoStart = valLower == "true" || valLower == "1" || valLower == "yes" || valLower == "on"
	}
	if val := os.Getenv("TALLY_1ERP_RELAY_ENABLED"); val != "" {
		valLower := strings.ToLower(strings.TrimSpace(val))
		cfg.Relay.Enabled = valLower == "true" || valLower == "1" || valLower == "yes" || valLower == "on"
	}
	if val := os.Getenv("TALLY_1ERP_SERVER_URL"); val != "" {
		cfg.Relay.ServerURL = strings.TrimRight(strings.TrimSpace(val), "/")
	}
	if val := os.Getenv("TALLY_1ERP_CREDENTIALS_FILE"); val != "" {
		cfg.Relay.CredentialsFile = strings.TrimSpace(val)
	}
	if val := os.Getenv("TALLY_1ERP_ALLOW_WRITES"); val != "" {
		valLower := strings.ToLower(strings.TrimSpace(val))
		cfg.Relay.AllowWrites = valLower == "true" || valLower == "1" || valLower == "yes" || valLower == "on"
	}
	if val := os.Getenv("TALLY_UPDATER_CHECK_INTERVAL_SECONDS"); val != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil && n >= 0 {
			cfg.Updater.CheckIntervalSeconds = n
		}
	}
	if val := os.Getenv("TALLY_UPDATER_AUTO_APPLY"); val != "" {
		valLower := strings.ToLower(strings.TrimSpace(val))
		cfg.Updater.AutoApply = valLower == "true" || valLower == "1" || valLower == "yes" || valLower == "on"
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
  # Default serialization format for tool responses: "toon" (Token-Oriented Object Notation)
  payload_format: "toon"

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
  # In-memory cache TTL for master data in seconds (instant sub-millisecond repeated queries)
  cache_ttl_seconds: 30
  # Optional path to tally.exe / TallyPrime executable (e.g. "C:\\Program Files\\TallyPrime\\tally.exe")
  # If left empty, standard installation directories are automatically searched.
  exe_path: ""
  # Automatically start Tally if not running when an MCP request arrives or when server starts
  auto_start: false

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
  # Interval in seconds to check GitHub Releases for new updates (default: 300 = 5 minutes)
  check_interval_seconds: 300
  # Automatically download, install, and restart if a new version is detected
  auto_apply: true

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
