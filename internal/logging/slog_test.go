package logging_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/logging"
)

func TestNewLogger_TextHandler(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger, err := logging.NewLogger(config.LogConfig{Level: "info", Format: "text"}, &buf)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}

	logger.Info("hello", slog.String("user", "lex"))

	output := buf.String()
	if !strings.Contains(output, "hello") {
		t.Errorf("text output missing message: %q", output)
	}
	if !strings.Contains(output, "user=lex") {
		t.Errorf("text output missing structured key: %q", output)
	}
}

func TestNewLogger_JSONHandler(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger, err := logging.NewLogger(config.LogConfig{Level: "info", Format: "json"}, &buf)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}

	logger.Info("hello", slog.String("user", "lex"))

	output := buf.String()
	if !strings.HasPrefix(strings.TrimSpace(output), "{") {
		t.Errorf("json output does not start with '{': %q", output)
	}
	if !strings.Contains(output, `"user":"lex"`) {
		t.Errorf("json output missing structured key: %q", output)
	}
}

func TestNewLogger_LevelFilters(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger, err := logging.NewLogger(config.LogConfig{Level: "warn", Format: "text"}, &buf)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}

	logger.Debug("below threshold")
	logger.Info("below threshold")
	logger.Warn("at threshold")
	logger.Error("above threshold")

	output := buf.String()
	if strings.Contains(output, "below threshold") {
		t.Errorf("warn level emitted below-threshold records: %q", output)
	}
	if !strings.Contains(output, "at threshold") {
		t.Errorf("warn record missing from output: %q", output)
	}
	if !strings.Contains(output, "above threshold") {
		t.Errorf("error record missing from output: %q", output)
	}
}

func TestNewLogger_InvalidLevel(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	_, err := logging.NewLogger(config.LogConfig{Level: "trace", Format: "text"}, &buf)
	if !errors.Is(err, logging.ErrInvalidLogConfig) {
		t.Errorf("NewLogger error = %v, want wrapping ErrInvalidLogConfig", err)
	}
}

func TestNewLogger_InvalidFormat(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	_, err := logging.NewLogger(config.LogConfig{Level: "info", Format: "xml"}, &buf)
	if !errors.Is(err, logging.ErrInvalidLogConfig) {
		t.Errorf("NewLogger error = %v, want wrapping ErrInvalidLogConfig", err)
	}
}
