package mcpmw_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/lexfrei/pogo-pvp-mcp/internal/mcpmw"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeHeavyToolCall builds a synthetic *mcp.CallToolRequest with
// method="tools/call" and the given tool name, for driving the
// middleware without spinning a full MCP Server.
func fakeToolCall(name string) (string, mcp.Request) {
	return "tools/call", &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: name},
	}
}

// TestTimeout_HeavyToolGetsLongerBudget pins the budget selection
// rule: a method matching heavyTools runs under the heavy budget;
// everything else runs under the default. Proven by a slow handler
// that sleeps until ctx.Done, then reports the budget the context
// carried.
func TestTimeout_HeavyToolGetsLongerBudget(t *testing.T) {
	t.Parallel()

	// Choose short budgets so the test is fast. 20 ms vs 200 ms —
	// 10x gap, well above scheduler noise.
	mw := mcpmw.Timeout(20*time.Millisecond, 200*time.Millisecond)

	measure := func(method string, req mcp.Request) time.Duration {
		handler := mw(func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			<-ctx.Done()

			return nil, ctx.Err()
		})

		start := time.Now()
		_, _ = handler(t.Context(), method, req)

		return time.Since(start)
	}

	// Default (non-heavy) method.
	method, req := fakeToolCall("pvp_rank")
	defaultElapsed := measure(method, req)

	// Heavy method.
	method, req = fakeToolCall("pvp_team_builder")
	heavyElapsed := measure(method, req)

	// Default should be <= ~50 ms (20 ms budget + jitter), heavy
	// should be >= ~180 ms (200 ms budget less jitter). The exact
	// thresholds are generous to avoid flakes on loaded CI.
	if defaultElapsed > 100*time.Millisecond {
		t.Errorf("default tool elapsed = %v, want close to 20 ms budget", defaultElapsed)
	}

	if heavyElapsed < 150*time.Millisecond {
		t.Errorf("heavy tool elapsed = %v, want close to 200 ms budget", heavyElapsed)
	}
}

// TestTimeout_ZeroBudgetDisables pins the opt-out: zero / negative
// budget on either tier skips the WithTimeout wrapper so the
// downstream handler runs under the caller's unmodified ctx.
func TestTimeout_ZeroBudgetDisables(t *testing.T) {
	t.Parallel()

	mw := mcpmw.Timeout(0, 0)

	called := false
	handler := mw(func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		called = true

		if _, hasDeadline := ctx.Deadline(); hasDeadline {
			t.Errorf("ctx has deadline; zero budget should skip WithTimeout wrapping")
		}

		return nil, nil //nolint:nilnil // synthetic test handler, no meaningful result
	})

	_, err := handler(t.Context(), "tools/call", &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "pvp_rank"},
	})
	if err != nil {
		t.Errorf("handler err = %v, want nil", err)
	}

	if !called {
		t.Error("handler not invoked")
	}
}

// TestLogging_SuccessRecordsInfoLevel pins the happy-path log
// entry: Info level, method + tool fields populated, duration_ms
// present and non-negative, no error field.
func TestLogging_SuccessRecordsInfoLevel(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mw := mcpmw.Logging(logger)
	handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, nil //nolint:nilnil // synthetic test handler
	})

	_, err := handler(t.Context(), "tools/call", &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "pvp_rank"},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	got := buf.String()
	wants := []string{"mcp method ok", "level=INFO", "method=tools/call", "tool=pvp_rank", "duration_ms="}

	for _, want := range wants {
		if !contains(got, want) {
			t.Errorf("log entry missing %q; got:\n%s", want, got)
		}
	}
}

// TestLogging_FailureRecordsErrorLevelAndTimedOutFlag pins the
// error-path log entry: ERROR level, error text captured, and the
// timed_out boolean is true iff the error wraps
// context.DeadlineExceeded. Operators use timed_out to distinguish
// tool bugs from too-tight timeout budgets.
func TestLogging_FailureRecordsErrorLevelAndTimedOutFlag(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mw := mcpmw.Logging(logger)
	handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, context.DeadlineExceeded
	})

	_, err := handler(t.Context(), "tools/call", &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "pvp_team_builder"},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("handler err = %v, want context.DeadlineExceeded", err)
	}

	got := buf.String()
	wants := []string{
		"mcp method failed", "level=ERROR",
		"tool=pvp_team_builder", "timed_out=true",
	}

	for _, want := range wants {
		if !contains(got, want) {
			t.Errorf("log entry missing %q; got:\n%s", want, got)
		}
	}
}

// TestLogging_NilLoggerDefaults covers the defensive contract: a
// nil logger falls back to slog.Default so production wiring
// doesn't need to special-case a zero logger, and tests can pass
// nil for brevity.
func TestLogging_NilLoggerDefaults(t *testing.T) {
	t.Parallel()

	mw := mcpmw.Logging(nil) // must not panic
	handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, nil //nolint:nilnil // synthetic test handler
	})

	_, err := handler(t.Context(), "tools/call", &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "pvp_rank"},
	})
	if err != nil {
		t.Errorf("handler err = %v, want nil", err)
	}
}

// contains is a trivial substring helper; avoids the stdlib import
// so the test file's import list stays short.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}

	return false
}
