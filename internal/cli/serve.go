package cli

import (
	"context"
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

// runServe orchestrates the full serve pipeline: load/refresh
// gamemaster, register tools, install signal handlers, and run the
// stdio transport until it errors or is cancelled.
func runServe(parent context.Context, rt *Runtime) error {
	mgr, err := gamemaster.NewManager(rt.Config.Gamemaster)
	if err != nil {
		return fmt.Errorf("gamemaster manager: %w", err)
	}

	primeGamemaster(parent, rt.Logger, mgr)

	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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
// falls back to a fresh upstream fetch. Either failing is logged but
// not fatal — the server starts anyway and the refresh loop will
// retry.
func primeGamemaster(ctx context.Context, logger *slog.Logger, mgr *gamemaster.Manager) {
	err := mgr.LoadLocal()
	if err == nil {
		logger.Info("loaded gamemaster from local cache")

		return
	}

	logger.Warn("local cache miss, falling back to upstream refresh",
		slog.String("error", err.Error()))

	err = mgr.Refresh(ctx)
	if err != nil {
		logger.Error("initial gamemaster refresh failed",
			slog.String("error", err.Error()))

		return
	}

	logger.Info("primed gamemaster from upstream")
}

// runRefreshLoop polls the gamemaster on the configured cadence until
// the context is cancelled.
func runRefreshLoop(
	ctx context.Context, logger *slog.Logger, mgr *gamemaster.Manager, interval time.Duration,
) {
	if interval <= 0 {
		logger.Info("refresh loop disabled (non-positive interval)")

		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := mgr.Refresh(ctx)
			if err != nil {
				logger.Warn("gamemaster refresh failed",
					slog.String("error", err.Error()))

				continue
			}

			logger.Debug("gamemaster refreshed")
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
