// Package mcpmw implements SDK-level middleware wrappers for
// MethodHandler — the MCP Server's inner dispatch unit. Where
// net/http middleware (internal/httpmw) protects the transport,
// mcpmw protects the business logic: per-method context timeouts
// and structured slog traces for every tool call.
//
// Two layers compose cleanly via mcp.Server.AddReceivingMiddleware,
// outer-to-inner: Logging → Timeout → tool handler. Logging
// captures wall-clock duration including any ctx-cancelled error
// surfaced by Timeout.
package mcpmw

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// heavyTools is the hard-coded set of method names that get the
// longer Timeout budget. Team-sweep tools legitimately run tens of
// seconds on realistic pools; everything else should finish in
// single-digit seconds. Kept as a map (not a slice) so the lookup
// stays O(1) on the hot path.
//
//nolint:gochecknoglobals // read-only lookup table
var heavyTools = map[string]struct{}{
	"pvp_team_builder":    {},
	"pvp_team_analysis":   {},
	"pvp_threat_coverage": {},
	"pvp_counter_finder":  {},
	"pvp_rank_batch":      {},
}

// Timeout returns a Middleware that wraps every incoming method
// call in context.WithTimeout. Heavy methods (see heavyTools) get
// heavyBudget; everything else gets defaultBudget. Either budget
// may be 0 or negative to disable the wrapper for that tier —
// useful for tests where arbitrary-length runs are acceptable.
//
// Timeout runs INSIDE the Logging wrapper (when both are composed)
// so a ctx-cancelled error shows up in the log entry as the method
// failure cause.
func Timeout(defaultBudget, heavyBudget time.Duration) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			budget := defaultBudget
			if _, heavy := heavyTools[extractToolName(method, req)]; heavy {
				budget = heavyBudget
			}

			if budget <= 0 {
				return next(ctx, method, req)
			}

			ctx, cancel := context.WithTimeout(ctx, budget)
			defer cancel()

			return next(ctx, method, req)
		}
	}
}

// Logging returns a Middleware that emits a structured log entry
// per method call. Fields: method (the MCP method — tools/call
// etc.), tool (the specific tool name for tools/call; empty
// otherwise), duration_ms (wall clock), and error (absent on
// success). Level is Info on success, Error on failure.
//
// Logger==nil falls back to slog.Default so production wiring
// doesn't need to special-case a zero logger. Note that
// context.Canceled / context.DeadlineExceeded are logged as
// errors — those signal either a misbehaving client or a Timeout
// middleware intervention; either is operator-interesting.
func Logging(logger *slog.Logger) mcp.Middleware {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			start := time.Now()
			result, err := next(ctx, method, req)
			durMS := time.Since(start).Milliseconds()

			tool := extractToolName(method, req)

			if err != nil {
				logger.LogAttrs(ctx, slog.LevelError, "mcp method failed",
					slog.String("method", method),
					slog.String("tool", tool),
					slog.Int64("duration_ms", durMS),
					slog.String("error", err.Error()),
					slog.Bool("timed_out", errors.Is(err, context.DeadlineExceeded)))
			} else {
				logger.LogAttrs(ctx, slog.LevelInfo, "mcp method ok",
					slog.String("method", method),
					slog.String("tool", tool),
					slog.Int64("duration_ms", durMS))
			}

			return result, err
		}
	}
}

// extractToolName returns the tool-name for a tools/call request,
// or an empty string for any other method. The SDK passes the raw
// Request through middleware; for tools/call it is a
// *mcp.CallToolParams with a .Name field. Any future request
// shapes fall through to the empty string — the field is advisory
// (logged as `tool=""`), never load-bearing for control flow.
func extractToolName(method string, req mcp.Request) string {
	if method != "tools/call" {
		return ""
	}

	params, ok := req.(*mcp.CallToolRequest)
	if !ok {
		return ""
	}

	if params.Params == nil {
		return ""
	}

	return params.Params.Name
}
