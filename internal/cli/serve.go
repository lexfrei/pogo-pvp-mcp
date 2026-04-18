package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// serverName is what the server advertises to MCP clients.
const serverName = "pogo-pvp-mcp"

// serverVersion is settable via -ldflags "-X
// github.com/lexfrei/pogo-pvp-mcp/internal/cli.serverVersion=..." at
// build time; the default "dev" applies to tests and go-run invocations.
//
//nolint:gochecknoglobals // ldflags injection target, must be a var
var serverVersion = "dev"

// refreshGrace is how long Run waits for the background refresh loop
// to exit during shutdown before giving up and returning.
const refreshGrace = 2 * time.Second

// bootstrapRetries is how many times primeGamemaster retries the
// upstream fetch during cold start before giving up and returning an
// error. Bootstrap failures are fatal because the main RefreshInterval
// (24h by default) is way too long to wait for a first successful
// fetch; better to surface the problem at start-up than silently serve
// "gamemaster not loaded" for a day.
const bootstrapRetries = 3

// bootstrapBackoffBase is the first retry delay; each subsequent retry
// multiplies by bootstrapBackoffFactor.
const bootstrapBackoffBase = 2 * time.Second

// bootstrapBackoffFactor is the multiplier applied to the retry delay
// on each successive attempt.
const bootstrapBackoffFactor = 4

// newServeCommand returns the "serve" subcommand. It sets up the
// gamemaster manager, the MCP server, and the stdio transport, then
// blocks until SIGINT / SIGTERM or a transport error.
func newServeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the MCP server over stdio",
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt := runtimeFrom(cmd.Context())

			return runServe(cmd.Context(), rt)
		},
	}
}

// runServe orchestrates the full serve pipeline: install signal
// handlers up-front, load/refresh gamemaster (with retries), register
// tools, and run the stdio transport until it errors or is cancelled.
// Signal handling is installed BEFORE primeGamemaster so a user can
// Ctrl+C out of a slow/failing cold-start fetch.
func runServe(parent context.Context, rt *Runtime) error {
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mgr, err := gamemaster.NewManager(rt.Config.Gamemaster)
	if err != nil {
		return fmt.Errorf("gamemaster manager: %w", err)
	}

	err = primeGamemaster(ctx, rt.Logger, mgr)
	if err != nil {
		return fmt.Errorf("prime gamemaster: %w", err)
	}

	server := buildMCPServer(mgr)

	refreshDone := make(chan struct{})

	go func() {
		defer close(refreshDone)

		runRefreshLoop(ctx, rt.Logger, mgr, rt.Config.Gamemaster.RefreshInterval)
	}()

	rt.Logger.Info("starting stdio transport",
		slog.String("transport", rt.Config.Server.Transport))

	err = server.Run(ctx, &mcp.StdioTransport{})

	stop()

	select {
	case <-refreshDone:
	case <-time.After(refreshGrace):
		rt.Logger.Warn("refresh loop did not exit within grace window")
	}

	if err != nil {
		return fmt.Errorf("mcp transport: %w", err)
	}

	return nil
}

// primeGamemaster tries the local cache first (fast, no network) and
// falls back to upstream fetch with bounded exponential backoff. A
// cold start with no cache and a failing upstream would otherwise
// leave the server returning ErrGamemasterNotLoaded until the next
// background refresh tick (24h by default), so a hard failure at
// start-up is preferable.
func primeGamemaster(ctx context.Context, logger *slog.Logger, mgr *gamemaster.Manager) error {
	err := mgr.LoadLocal()
	if err == nil {
		logger.Info("loaded gamemaster from local cache")

		return nil
	}

	logger.Warn("local cache miss, falling back to upstream refresh",
		slog.String("error", err.Error()))

	delay := bootstrapBackoffBase

	var lastErr error

	for attempt := 1; attempt <= bootstrapRetries; attempt++ {
		lastErr = mgr.Refresh(ctx)
		if lastErr == nil {
			logger.Info("primed gamemaster from upstream",
				slog.Int("attempt", attempt))

			return nil
		}

		logger.Warn("bootstrap refresh attempt failed",
			slog.Int("attempt", attempt),
			slog.Int("max_attempts", bootstrapRetries),
			slog.String("error", lastErr.Error()))

		if attempt == bootstrapRetries {
			break
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("bootstrap cancelled: %w", ctx.Err())
		case <-time.After(delay):
			delay *= bootstrapBackoffFactor
		}
	}

	return fmt.Errorf("initial refresh failed after %d attempts: %w",
		bootstrapRetries, lastErr)
}

// runRefreshLoop polls the gamemaster on the configured cadence until
// the context is cancelled. The caller must have loaded a config that
// passed Config.Validate — which enforces a strictly positive
// RefreshInterval — so no "disabled" branch is needed here.
func runRefreshLoop(
	ctx context.Context, logger *slog.Logger, mgr *gamemaster.Manager, interval time.Duration,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := mgr.Refresh(ctx)
			if err == nil {
				logger.Debug("gamemaster refreshed")

				continue
			}

			if errors.Is(err, context.Canceled) {
				return
			}

			logger.Warn("gamemaster refresh failed",
				slog.String("error", err.Error()))
		}
	}
}

// buildMCPServer constructs the mcp.Server with the two Phase 3 tools
// (pvp_rank and pvp_matchup) registered.
func buildMCPServer(mgr *gamemaster.Manager) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, nil)

	rankTool := tools.NewRankTool(mgr)
	mcp.AddTool(server, rankTool.Tool(), rankTool.Handler())

	matchupTool := tools.NewMatchupTool(mgr)
	mcp.AddTool(server, matchupTool.Tool(), matchupTool.Handler())

	return server
}
