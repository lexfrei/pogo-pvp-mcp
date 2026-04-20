// Package config loads and validates the MCP server's runtime
// configuration. Inputs are merged in the order defaults → YAML →
// environment (prefix POGO_PVP_) so higher-priority sources override
// lower ones. CLI flag binding is the caller's responsibility (the
// cobra setup in internal/cli).
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

// defaultRefreshInterval is how often the gamemaster fetcher polls
// upstream for a fresh copy.
const defaultRefreshInterval = 24 * time.Hour

// defaultGamemasterURL is the canonical pvpoke gamemaster location.
const defaultGamemasterURL = "https://raw.githubusercontent.com/pvpoke/pvpoke/master/src/data/gamemaster.json"

// defaultGamemasterFile is the filename used under the XDG cache
// directory when no local_path is configured.
const defaultGamemasterFile = "gamemaster.json"

// defaultCacheDirName is the subdirectory inside the XDG cache root.
const defaultCacheDirName = "pogo-pvp-mcp"

// defaultLocalPath returns the XDG-style cache path for the gamemaster
// file. Falls back to ~/.cache/pogo-pvp-mcp/gamemaster.json when
// XDG_CACHE_HOME is unset, and to the current working directory's
// ./gamemaster.json if the home directory is also unknown. The function
// never returns an empty string so NewManager's invariant stays intact.
func defaultLocalPath() string {
	cacheRoot := os.Getenv("XDG_CACHE_HOME")
	if cacheRoot == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			cacheRoot = filepath.Join(home, ".cache")
		}
	}

	if cacheRoot == "" {
		return defaultGamemasterFile
	}

	return filepath.Join(cacheRoot, defaultCacheDirName, defaultGamemasterFile)
}

// Config is the full runtime configuration consumed by the server. The
// mapstructure tags let viper unmarshal YAML and env sources into the
// same struct without hand-written glue. Caching is deliberately not a
// configured field yet: the LRU implementation in internal/cache is
// ready but not wired into the tool handlers. When caching lands the
// config will grow a cache.size knob; exposing one prematurely would be
// documentation drift.
type Config struct {
	Server     ServerConfig     `mapstructure:"server"`
	Log        LogConfig        `mapstructure:"log"`
	Gamemaster GamemasterConfig `mapstructure:"gamemaster"`
	Engine     EngineConfig     `mapstructure:"engine"`
}

// ServerConfig covers transport selection, the debug HTTP surface, and
// the public MCP HTTP endpoint.
//
// Transport / HTTPHost / HTTPPort govern the LOOPBACK debug server
// (/healthz, POST /refresh, /debug/gamemaster). That server is not
// intended to be exposed publicly — its endpoints include unauth'd
// mutations (refresh) and diagnostics dumps.
//
// MCPHTTPListen governs the PUBLIC MCP endpoint (Streamable HTTP). It
// is bound on whatever address the operator chooses (":8080" or
// "0.0.0.0:8080" typically) and is protected by the Phase 3 net/http
// middleware chain (recover → realIP → rateLimit → maxBytes). Empty
// = disabled; stdio remains the only transport.
//
// Phase 3 controls:
//
//   - TrustedProxies: CIDR list of trusted reverse proxies. When a
//     request arrives from a trusted source address, the first
//     X-Forwarded-For entry is treated as the effective client IP
//     (used for rate-limit keying and logging). Empty = ignore XFF.
//   - RateLimitRPS / RateLimitBurst: per-client-IP token-bucket
//     parameters. RPS=0 disables rate limiting entirely (safe for
//     dev; never use in prod).
//   - MaxRequestBytes: body-size cap via http.MaxBytesReader. 0 =
//     disabled (safe for dev).
type ServerConfig struct {
	Transport       string   `mapstructure:"transport"`
	HTTPHost        string   `mapstructure:"http_host"`
	HTTPPort        int      `mapstructure:"http_port"`
	MCPHTTPListen   string   `mapstructure:"mcp_http_listen"`
	TrustedProxies  []string `mapstructure:"trusted_proxies"`
	RateLimitRPS    int      `mapstructure:"rate_limit_rps"`
	RateLimitBurst  int      `mapstructure:"rate_limit_burst"`
	MaxRequestBytes int64    `mapstructure:"max_request_bytes"`
}

// LogConfig toggles slog output level and format.
type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
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
	view.SetDefault("server.mcp_http_listen", "")
	view.SetDefault("server.trusted_proxies", []string{})
	view.SetDefault("server.rate_limit_rps", defaultRateLimitRPS)
	view.SetDefault("server.rate_limit_burst", defaultRateLimitBurst)
	view.SetDefault("server.max_request_bytes", defaultMaxRequestBytes)

	view.SetDefault("log.level", "info")
	view.SetDefault("log.format", "text")

	view.SetDefault("gamemaster.source", defaultGamemasterURL)
	view.SetDefault("gamemaster.local_path", defaultLocalPath())
	view.SetDefault("gamemaster.refresh_interval", defaultRefreshInterval)

	view.SetDefault("engine.goroutines", 0)
}

// validTransports lists the supported [ServerConfig.Transport] values.
// Only stdio is wired today; http will be added when the debug HTTP
// transport lands. Listing unimplemented options here would silently
// mis-wire the server, so the list stays minimal.
//
//nolint:gochecknoglobals // read-only lookup table
var validTransports = []string{"stdio"}

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

// defaultRateLimitRPS is the per-client-IP request rate. Low enough
// to stop naive abusers from exhausting team_builder capacity; high
// enough that a legitimate LLM client session (dozens of tool calls
// per user turn) is never rate-limited.
const defaultRateLimitRPS = 10

// defaultRateLimitBurst is the token-bucket burst budget. Headroom
// for a legitimate client to send its tools/list + several tool
// calls in a single burst before settling into the RPS pace.
const defaultRateLimitBurst = 20

// defaultMaxRequestBytes caps the public MCP HTTP body size. MCP
// requests are small — 64 KiB is more than enough for a pool of 50
// combatants with full Options blocks; malicious oversized posts are
// cut off before they reach the handler.
const defaultMaxRequestBytes int64 = 64 * 1024

// Validate reports whether every field is consistent. Callers usually
// receive this via [Load]; hand calls exist for tests that mutate a
// loaded [Config] in-place.
func (c *Config) Validate() error {
	err := c.validateServerAndLog()
	if err != nil {
		return err
	}

	return c.validateDataPlane()
}

// validateServerAndLog covers the transport/log/port enum checks.
func (c *Config) validateServerAndLog() error {
	err := c.validateTransportAndDebug()
	if err != nil {
		return err
	}

	err = c.validateMCPHTTPPhase3()
	if err != nil {
		return err
	}

	return c.validateLog()
}

// validateTransportAndDebug covers the stdio / debug HTTP surface:
// transport enum + debug listener port range.
func (c *Config) validateTransportAndDebug() error {
	if !slices.Contains(validTransports, c.Server.Transport) {
		return fmt.Errorf("%w: server.transport=%q, want one of %v",
			ErrInvalidConfig, c.Server.Transport, validTransports)
	}

	if c.Server.HTTPPort < 0 || c.Server.HTTPPort > maxPort {
		return fmt.Errorf("%w: server.http_port=%d outside [0, %d]",
			ErrInvalidConfig, c.Server.HTTPPort, maxPort)
	}

	return nil
}

// validateMCPHTTPPhase3 covers the public MCP HTTP listener plus the
// Phase 3 middleware controls (rate limit, body cap, trusted
// proxies). Each control has a "0 / empty = disabled" form; negative
// is rejected.
func (c *Config) validateMCPHTTPPhase3() error {
	if c.Server.MCPHTTPListen != "" {
		err := validateListenAddress(c.Server.MCPHTTPListen)
		if err != nil {
			return fmt.Errorf("%w: server.mcp_http_listen=%q: %w",
				ErrInvalidConfig, c.Server.MCPHTTPListen, err)
		}
	}

	if c.Server.RateLimitRPS < 0 {
		return fmt.Errorf("%w: server.rate_limit_rps=%d must be non-negative",
			ErrInvalidConfig, c.Server.RateLimitRPS)
	}

	if c.Server.RateLimitBurst < 0 {
		return fmt.Errorf("%w: server.rate_limit_burst=%d must be non-negative",
			ErrInvalidConfig, c.Server.RateLimitBurst)
	}

	if c.Server.MaxRequestBytes < 0 {
		return fmt.Errorf("%w: server.max_request_bytes=%d must be non-negative",
			ErrInvalidConfig, c.Server.MaxRequestBytes)
	}

	for _, cidr := range c.Server.TrustedProxies {
		_, _, err := net.ParseCIDR(cidr)
		if err != nil {
			return fmt.Errorf("%w: server.trusted_proxies=%q: %w",
				ErrInvalidConfig, cidr, err)
		}
	}

	return nil
}

// validateLog checks the slog handler selection.
func (c *Config) validateLog() error {
	if !slices.Contains(validLogLevels, c.Log.Level) {
		return fmt.Errorf("%w: log.level=%q, want one of %v",
			ErrInvalidConfig, c.Log.Level, validLogLevels)
	}

	if !slices.Contains(validLogFormats, c.Log.Format) {
		return fmt.Errorf("%w: log.format=%q, want one of %v",
			ErrInvalidConfig, c.Log.Format, validLogFormats)
	}

	return nil
}

// errListenAddressEmpty signals the caller passed "" to
// validateListenAddress. Callers of Config.Validate guard against
// this before calling, but the helper is still exported-style
// defensive: the validator should never claim "" is a valid listen
// address (that form means "disabled").
var errListenAddressEmpty = errors.New("listen address is empty")

// errListenAddressInvalid covers every non-parse listen-address
// rejection: empty port, port out of range, non-numeric port. Wrapped
// with fmt.Errorf so the caller sees the specific value, while the
// linter stops complaining about dynamic error construction.
var errListenAddressInvalid = errors.New("listen address invalid")

// validateListenAddress parses a Go listen-address string
// ("host:port", ":port", "[ipv6]:port") and reports whether it is
// acceptable for net.Listen. Checks (beyond net.SplitHostPort):
//
//   - Port must be non-empty and a decimal integer in the half-open
//     range (0, maxPort]. Port 0 is permitted at net.Listen time
//     (kernel picks), but explicit 0 in config is nonsense (the
//     listener would rebind every restart), so we additionally
//     reject 0 here to align with the operator's intent when they
//     hand-set the field.
//
// Host is deliberately not validated beyond SplitHostPort's parse —
// DNS names and IPv6 literals both round-trip through SplitHostPort,
// and resolving DNS at config-load time would add a network
// dependency that config validation should not have.
func validateListenAddress(addr string) error {
	if addr == "" {
		return errListenAddressEmpty
	}

	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%w: split host/port: %w", errListenAddressInvalid, err)
	}

	if portStr == "" {
		return fmt.Errorf("%w: port is empty", errListenAddressInvalid)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("%w: port %q: %w", errListenAddressInvalid, portStr, err)
	}

	if port <= 0 || port > maxPort {
		return fmt.Errorf("%w: port %d outside (0, %d]", errListenAddressInvalid, port, maxPort)
	}

	return nil
}

// validateDataPlane covers the engine / gamemaster invariants.
func (c *Config) validateDataPlane() error {
	if c.Engine.Goroutines < 0 {
		return fmt.Errorf("%w: engine.goroutines=%d must be non-negative",
			ErrInvalidConfig, c.Engine.Goroutines)
	}

	if c.Gamemaster.RefreshInterval <= 0 {
		return fmt.Errorf("%w: gamemaster.refresh_interval=%v must be positive",
			ErrInvalidConfig, c.Gamemaster.RefreshInterval)
	}

	if c.Gamemaster.Source == "" {
		return fmt.Errorf("%w: gamemaster.source must not be empty", ErrInvalidConfig)
	}

	if c.Gamemaster.LocalPath == "" {
		return fmt.Errorf("%w: gamemaster.local_path must not be empty", ErrInvalidConfig)
	}

	return nil
}
