package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
)

const transportStdio = "stdio"

func TestLoad_Defaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.Transport != transportStdio {
		t.Errorf("Server.Transport = %q, want %q", cfg.Server.Transport, transportStdio)
	}
	if cfg.Server.HTTPHost != "127.0.0.1" {
		t.Errorf("Server.HTTPHost = %q, want \"127.0.0.1\"", cfg.Server.HTTPHost)
	}
	if cfg.Server.HTTPPort != 0 {
		t.Errorf("Server.HTTPPort = %d, want 0 (disabled)", cfg.Server.HTTPPort)
	}
	if cfg.Server.MCPHTTPListen != "" {
		t.Errorf("Server.MCPHTTPListen = %q, want empty (disabled)", cfg.Server.MCPHTTPListen)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level = %q, want \"info\"", cfg.Log.Level)
	}
	if cfg.Log.Format != "text" {
		t.Errorf("Log.Format = %q, want \"text\"", cfg.Log.Format)
	}
	if cfg.Gamemaster.RefreshInterval.Hours() != 24 {
		t.Errorf("Gamemaster.RefreshInterval = %v, want 24h", cfg.Gamemaster.RefreshInterval)
	}
}

func TestLoad_YAMLFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
server:
  transport: stdio
  http_port: 9090
log:
  level: debug
  format: json
gamemaster:
  refresh_interval: 2h
engine:
  goroutines: 4
`
	err := os.WriteFile(path, []byte(yaml), 0o600)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.Transport != transportStdio {
		t.Errorf("Server.Transport = %q, want %q", cfg.Server.Transport, transportStdio)
	}
	if cfg.Server.HTTPPort != 9090 {
		t.Errorf("Server.HTTPPort = %d, want 9090", cfg.Server.HTTPPort)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("Log.Level = %q, want \"debug\"", cfg.Log.Level)
	}
	if cfg.Log.Format != "json" {
		t.Errorf("Log.Format = %q, want \"json\"", cfg.Log.Format)
	}
	if cfg.Gamemaster.RefreshInterval.Hours() != 2 {
		t.Errorf("Gamemaster.RefreshInterval = %v, want 2h", cfg.Gamemaster.RefreshInterval)
	}
	if cfg.Engine.Goroutines != 4 {
		t.Errorf("Engine.Goroutines = %d, want 4", cfg.Engine.Goroutines)
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	t.Setenv("POGO_PVP_LOG_LEVEL", "warn")
	t.Setenv("POGO_PVP_SERVER_HTTP_PORT", "9999")
	t.Setenv("POGO_PVP_SERVER_MCP_HTTP_LISTEN", "0.0.0.0:8080")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Log.Level != "warn" {
		t.Errorf("Log.Level = %q, want \"warn\" (from env)", cfg.Log.Level)
	}
	if cfg.Server.HTTPPort != 9999 {
		t.Errorf("Server.HTTPPort = %d, want 9999 (from env)", cfg.Server.HTTPPort)
	}
	if cfg.Server.MCPHTTPListen != "0.0.0.0:8080" {
		t.Errorf("Server.MCPHTTPListen = %q, want \"0.0.0.0:8080\" (from env)",
			cfg.Server.MCPHTTPListen)
	}
}

func TestValidate_InvalidTransport(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Server.Transport = "smoke-signal"
	err = cfg.Validate()
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Errorf("Validate() = %v, want wrapping ErrInvalidConfig", err)
	}
}

func TestValidate_InvalidLogLevel(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Log.Level = "verbose"
	err = cfg.Validate()
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Errorf("Validate() = %v, want wrapping ErrInvalidConfig", err)
	}
}

func TestValidate_InvalidLogFormat(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Log.Format = "xml"
	err = cfg.Validate()
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Errorf("Validate() = %v, want wrapping ErrInvalidConfig", err)
	}
}

// TestValidate_InvalidMCPHTTPListen drives the Phase 1 round-1
// review finding: net.SplitHostPort alone accepts several strings
// (":", "localhost:bad", "host:99999") that then fail opaquely at
// net.Listen. Each subcase must surface ErrInvalidConfig at load
// time so misconfigurations don't silently boot.
func TestValidate_InvalidMCPHTTPListen(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
	}{
		{"missing_port", "not-a-valid-listen-address"},
		{"bare_colon", ":"},
		{"non_numeric_port", "localhost:bad"},
		{"port_above_max", "127.0.0.1:99999"},
		{"negative_port", "127.0.0.1:-1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := config.Load("")
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			cfg.Server.MCPHTTPListen = tc.value

			err = cfg.Validate()
			if !errors.Is(err, config.ErrInvalidConfig) {
				t.Errorf("Validate(%q) = %v, want wrapping ErrInvalidConfig", tc.value, err)
			}
		})
	}
}

// TestValidate_AcceptableMCPHTTPListen locks the forms that must
// survive validation. All are canonical Go listen-address spellings;
// dropping any of them is a breaking config regression.
func TestValidate_AcceptableMCPHTTPListen(t *testing.T) {
	t.Parallel()

	cases := []string{
		":8080",
		"0.0.0.0:8080",
		"127.0.0.1:18080",
		"[::]:8080",
		"[::1]:8080",
	}

	for _, value := range cases {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			cfg, err := config.Load("")
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			cfg.Server.MCPHTTPListen = value

			err = cfg.Validate()
			if err != nil {
				t.Errorf("Validate(%q) = %v, want nil", value, err)
			}
		})
	}
}

func TestValidate_EmptyMCPHTTPListenIsValid(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Server.MCPHTTPListen = ""

	err = cfg.Validate()
	if err != nil {
		t.Errorf("Validate() = %v, want nil (empty is the disabled form)", err)
	}
}

func TestValidate_InvalidHTTPPort(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Server.HTTPPort = 70000
	err = cfg.Validate()
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Errorf("Validate() = %v, want wrapping ErrInvalidConfig", err)
	}
}

func TestValidate_NegativeGoroutines(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Engine.Goroutines = -1
	err = cfg.Validate()
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Errorf("Validate() = %v, want wrapping ErrInvalidConfig", err)
	}
}

func TestValidate_ZeroRefreshInterval(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Gamemaster.RefreshInterval = 0

	err = cfg.Validate()
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Errorf("Validate() = %v, want wrapping ErrInvalidConfig", err)
	}
}

func TestValidate_EmptyLocalPath(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Gamemaster.LocalPath = ""

	err = cfg.Validate()
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Errorf("Validate() = %v, want wrapping ErrInvalidConfig", err)
	}
}

func TestValidate_EmptyGamemasterSource(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Gamemaster.Source = ""

	err = cfg.Validate()
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Errorf("Validate() = %v, want wrapping ErrInvalidConfig", err)
	}
}

// TestLoad_DefaultLocalPathIsNotEmpty pins the contract that out-of-
// the-box Load() returns a non-empty LocalPath so the gamemaster
// manager can start without requiring env/yaml overrides.
func TestLoad_DefaultLocalPathIsNotEmpty(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Gamemaster.LocalPath == "" {
		t.Error("default Gamemaster.LocalPath is empty — zero-config startup broken")
	}
}

func TestLoad_NonexistentExplicitPath(t *testing.T) {
	t.Parallel()

	_, err := config.Load("/definitely/not/here.yaml")
	if err == nil {
		t.Fatal("expected error when explicit config path does not exist")
	}
}
