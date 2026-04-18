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
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level = %q, want \"info\"", cfg.Log.Level)
	}
	if cfg.Log.Format != "text" {
		t.Errorf("Log.Format = %q, want \"text\"", cfg.Log.Format)
	}
	if cfg.Cache.Size != 32*1024*1024 {
		t.Errorf("Cache.Size = %d, want 32 MiB", cfg.Cache.Size)
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
cache:
  size: 1024
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
	if cfg.Cache.Size != 1024 {
		t.Errorf("Cache.Size = %d, want 1024", cfg.Cache.Size)
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
	t.Setenv("POGO_PVP_CACHE_SIZE", "2048")
	t.Setenv("POGO_PVP_SERVER_HTTP_PORT", "9999")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Log.Level != "warn" {
		t.Errorf("Log.Level = %q, want \"warn\" (from env)", cfg.Log.Level)
	}
	if cfg.Cache.Size != 2048 {
		t.Errorf("Cache.Size = %d, want 2048 (from env)", cfg.Cache.Size)
	}
	if cfg.Server.HTTPPort != 9999 {
		t.Errorf("Server.HTTPPort = %d, want 9999 (from env)", cfg.Server.HTTPPort)
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

func TestValidate_NegativeCacheSize(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Cache.Size = -1
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
