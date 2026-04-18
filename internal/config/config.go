// Package config loads and validates the MCP server's runtime
// configuration. Inputs are merged in the order dfeaults → YAML →
// environment (prefix POGO_PVP_) so higher-priority sources override
// lower ones. CLI flag binding is the caller's responsibility (the
// cobra setup in internal/cli).
package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// ErrInvalidConfig is returned by [Config.Validate] when any field
// carries a value outside its allowed set.
var ErrInvalidConfig = errors.New("invalid config")

// envPrefix scopes env variables for this process so hosting
// environments can set POGO_PVP_LOG_LEVEL without colliding with other
// tools sharing the same shell.
const envPrefix = "POGO_PVP"

// defaultCacheSize is 32 MiB, enough to hold a few thousand rank /
// matchup entries at typical payload sizes.
const defaultCacheSize = 32 * 1024 * 1024

// defaultRefreshInterval is how often the gamemaster fetcher polls
// upstream for a fresh copy.
const defaultRefreshInterval = 24 * time.Hour

// defaultGamemasterURL is the canonical pvpoke gamemaster location.
const defaultGamemasterURL = "https://raw.githubusercontent.com/pvpoke/pvpoke/master/src/data/gamemaster.json"

// Config is the full runtime configuration consumed by the server. The
// mapstructure tags let viper unmarshal YAML and env sources into the
// same struct without hand-written glue.
type Config struct {
	Server     ServerConfig     `mapstructure:"server"`
	Log        LogConfig        `mapstructure:"log"`
	Cache      CacheConfig      `mapstructure:"cache"`
	Gamemaster GamemasterConfig `mapstructure:"gamemaster"`
	Engine     EngineConfig     `mapstructure:"engine"`
}

// ServerConfig covers transport selection and debug HTTP surface.
type ServerConfig struct {
	Transport string `mapstructure:"transport"`
	HTTPHost  string `mapstructure:"http_host"`
	HTTPPort  int    `mapstructure:"http_port"`
}

// LogConfig toggles slog output level and format.
type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// CacheConfig bounds the in-memory LRU in bytes. Zero disables.
type CacheConfig struct {
	Size int `mapstructure:"size"`
}

// GamemasterConfig drives the upstream fetcher.
type GamemasterConfig struct {
	Source          string        `mapstructure:"source"`
	LocalPath       string        `mapstructure:"local_path"`
	RefreshInterval time.Duration `mapstructure:"refresh_interval"`
}

// EngineConfig tunes the parallel battle sim.
type EngineConfig struct {
	// Goroutines caps the worker pool; 0 means runtime.NumCPU() at
	// server start-up.
	Goroutines int `mapstructure:"goroutines"`
}

// Load reads configuration from the given YAML file (empty path means
// defaults + env only), applies defaults, and returns a validated
// [Config]. A non-empty path that does not exist is an error so
// misspelled --config flags fail loud instead of silently defaulting.
func Load(path string) (*Config, error) {
	view := viper.New()
	applyDefaults(view)

	view.SetEnvPrefix(envPrefix)
	view.AutomaticEnv()
	view.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if path != "" {
		view.SetConfigFile(path)

		err := view.ReadInConfig()
		if err != nil {
			return nil, fmt.Errorf("read config %q: %w", path, err)
		}
	}

	var cfg Config

	err := view.Unmarshal(&cfg)
	if err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	err = cfg.Validate()
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}

// applyDefaults seeds the viper instance with the baseline values that
// match the YAML schema exactly — any field not explicitly set by the
// user falls through to these.
func applyDefaults(view *viper.Viper) {
	view.SetDefault("server.transport", "stdio")
	view.SetDefault("server.http_host", "127.0.0.1")
	view.SetDefault("server.http_port", 0)

	view.SetDefault("log.level", "info")
	view.SetDefault("log.format", "text")

	view.SetDefault("cache.size", defaultCacheSize)

	view.SetDefault("gamemaster.source", defaultGamemasterURL)
	view.SetDefault("gamemaster.local_path", "")
	view.SetDefault("gamemaster.refresh_interval", defaultRefreshInterval)

	view.SetDefault("engine.goroutines", 0)
}

// validTransports lists the supported [ServerConfig.Transport] values.
//
//nolint:gochecknoglobals // read-only lookup table
var validTransports = []string{"stdio", "http"}

// validLogLevels lists the slog level names accepted by [LogConfig.Level].
//
//nolint:gochecknoglobals // read-only lookup table
var validLogLevels = []string{"debug", "info", "warn", "error"}

// validLogFormats lists the slog handler choices.
//
//nolint:gochecknoglobals // read-only lookup table
var validLogFormats = []string{"text", "json"}

// maxPort is the top of the TCP port range.
const maxPort = 65535

// Validate reports whether every field is consistent. Callers usually
// receive this via [Load]; hand calls exist for tests that mutate a
// loaded [Config] in-place.
func (c *Config) Validate() error {
	if !slices.Contains(validTransports, c.Server.Transport) {
		return fmt.Errorf("%w: server.transport=%q, want one of %v",
			ErrInvalidConfig, c.Server.Transport, validTransports)
	}

	if c.Server.HTTPPort < 0 || c.Server.HTTPPort > maxPort {
		return fmt.Errorf("%w: server.http_port=%d outside [0, %d]",
			ErrInvalidConfig, c.Server.HTTPPort, maxPort)
	}

	if !slices.Contains(validLogLevels, c.Log.Level) {
		return fmt.Errorf("%w: log.level=%q, want one of %v",
			ErrInvalidConfig, c.Log.Level, validLogLevels)
	}

	if !slices.Contains(validLogFormats, c.Log.Format) {
		return fmt.Errorf("%w: log.format=%q, want one of %v",
			ErrInvalidConfig, c.Log.Format, validLogFormats)
	}

	if c.Cache.Size < 0 {
		return fmt.Errorf("%w: cache.size=%d must be non-negative",
			ErrInvalidConfig, c.Cache.Size)
	}

	if c.Engine.Goroutines < 0 {
		return fmt.Errorf("%w: engine.goroutines=%d must be non-negative",
			ErrInvalidConfig, c.Engine.Goroutines)
	}

	return nil
}
