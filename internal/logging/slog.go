// Package logging builds the slog.Logger the server uses for its
// structured output. Text vs JSON handler and level are driven by
// [config.LogConfig]; the output writer is typically os.Stderr but the
// tests inject a bytes.Buffer.
package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
)

// ErrInvalidLogConfig is returned by [NewLogger] when the LogConfig
// carries a level or format string outside the validated enum.
var ErrInvalidLogConfig = errors.New("invalid log config")

// NewLogger constructs a slog.Logger from the given LogConfig writing to
// the provided writer. Returns ErrInvalidLogConfig when the level or
// format cannot be mapped to a supported slog handler. Config.Validate
// already rejects these at load time; this function re-checks so callers
// that construct a LogConfig directly still fail loud.
func NewLogger(cfg config.LogConfig, writer io.Writer) (*slog.Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	opts := &slog.HandlerOptions{Level: level}

	switch cfg.Format {
	case "text":
		return slog.New(slog.NewTextHandler(writer, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(writer, opts)), nil
	default:
		return nil, fmt.Errorf("%w: format %q not in {text, json}", ErrInvalidLogConfig, cfg.Format)
	}
}

// parseLevel maps a LogConfig level string to the slog level constant.
func parseLevel(name string) (slog.Level, error) {
	switch name {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("%w: level %q not in {debug, info, warn, error}", ErrInvalidLogConfig, name)
	}
}
