package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/lexfrei/pogo-pvp-mcp/internal/debug"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// defaultRankingsBaseURL is the pvpoke rankings-folder root on GitHub.
const defaultRankingsBaseURL = "https://raw.githubusercontent.com/pvpoke/pvpoke/master/src/data/rankings"

// rankingsSubdir is the per-process cache subdirectory for the
// rankings manager, sibling to the gamemaster cache file.
const rankingsSubdir = "rankings"

// serverName is what the server advertises to MCP clients.
const serverName = "pogo-pvp-mcp"

// serverVersion is settable via -ldflags "-X
// github.com/lexfrei/pogo-pvp-mcp/internal/cli.serverVersion=..." at
// build time; the default "dev" applies to tests and go-run invocations.
//
//nolint:gochecknoglobals // ldflags injection target, must be a var
var serverVersion = "dev"

// serverRevision carries the VCS revision (git sha) of the build.
// Populated via -ldflags "-X
// github.com/lexfrei/pogo-pvp-mcp/internal/cli.serverRevision=...";
// the default "unknown" applies to tests and go-run invocations.
//
//nolint:gochecknoglobals // ldflags injection target, must be a var
var serverRevision = "unknown"

// refreshGrace is how long Run waits for the background refresh loop
// to exit during shutdown before giving up and returning.
const refreshGrace = 2 * time.Second

// debugServerShutdownGrace is the window the HTTP debug server gets
// to finish in-flight requests after the main context is cancelled.
const debugServerShutdownGrace = 5 * time.Second

// debugServerReadHeaderTimeout is the read-header deadline for the
// debug HTTP server; keeps pathological clients from tying up a
// handler goroutine.
const debugServerReadHeaderTimeout = 10 * time.Second

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

	managers, err := buildManagers(rt)
	if err != nil {
		return err
	}

	err = primeGamemaster(ctx, rt.Logger, managers.Gamemaster)
	if err != nil {
		return fmt.Errorf("prime gamemaster: %w", err)
	}

	server := buildMCPServer(managers.Gamemaster, managers.Rankings)
	refreshDone := startRefreshLoop(ctx, rt, managers.Gamemaster)
	debugDone := startDebugServer(ctx, rt, managers.Gamemaster)

	rt.Logger.Info("starting stdio transport",
		slog.String("transport", rt.Config.Server.Transport),
		slog.String("version", serverVersion),
		slog.String("revision", serverRevision))

	err = server.Run(ctx, &mcp.StdioTransport{})

	stop()
	waitForBackgroundShutdown(rt, refreshDone, debugDone)

	if err != nil {
		return fmt.Errorf("mcp transport: %w", err)
	}

	return nil
}

// managerBundle groups the gamemaster and rankings managers so
// buildManagers can return them through a single typed value.
type managerBundle struct {
	Gamemaster *gamemaster.Manager
	Rankings   *rankings.Manager
}

// buildManagers constructs the gamemaster + rankings managers from
// the runtime config, placing the rankings cache next to the
// gamemaster cache so they share a parent directory.
func buildManagers(rt *Runtime) (*managerBundle, error) {
	mgr, err := gamemaster.NewManager(rt.Config.Gamemaster)
	if err != nil {
		return nil, fmt.Errorf("gamemaster manager: %w", err)
	}

	ranks, err := buildRankingsManager(rankings.Config{
		BaseURL:  defaultRankingsBaseURL,
		LocalDir: rankingsCacheDir(rt.Config.Gamemaster.LocalPath),
	})
	if err != nil {
		return nil, err
	}

	return &managerBundle{Gamemaster: mgr, Rankings: ranks}, nil
}

// startRefreshLoop launches the periodic gamemaster refresh goroutine
// and returns a channel that closes when the loop exits.
func startRefreshLoop(ctx context.Context, rt *Runtime, mgr *gamemaster.Manager) <-chan struct{} {
	done := make(chan struct{})

	go func() {
		defer close(done)

		runRefreshLoop(ctx, rt.Logger, mgr, rt.Config.Gamemaster.RefreshInterval)
	}()

	return done
}

// waitForBackgroundShutdown blocks until the refresh loop and the
// optional debug HTTP server have exited, each under its own grace
// window, and warns when either misses its deadline.
func waitForBackgroundShutdown(rt *Runtime, refreshDone, debugDone <-chan struct{}) {
	select {
	case <-refreshDone:
	case <-time.After(refreshGrace):
		rt.Logger.Warn("refresh loop did not exit within grace window")
	}

	if debugDone == nil {
		return
	}

	select {
	case <-debugDone:
	case <-time.After(debugServerShutdownGrace):
		rt.Logger.Warn("debug HTTP server did not exit within grace window")
	}
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

// buildMCPServer constructs the mcp.Server with all ten currently
// implemented tools registered (pvp_rank, pvp_matchup, pvp_cp_limits,
// pvp_meta, pvp_team_analysis, pvp_team_builder, pvp_species_info,
// pvp_move_info, pvp_type_matchup, pvp_level_from_cp).
func buildMCPServer(gamemasterMgr *gamemaster.Manager, ranks *rankings.Manager) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, nil)

	rankTool := tools.NewRankTool(gamemasterMgr, ranks)
	mcp.AddTool(server, rankTool.Tool(), rankTool.Handler())

	matchupTool := tools.NewMatchupTool(gamemasterMgr, ranks)
	mcp.AddTool(server, matchupTool.Tool(), matchupTool.Handler())

	cpLimitsTool := tools.NewCPLimitsTool(gamemasterMgr)
	mcp.AddTool(server, cpLimitsTool.Tool(), cpLimitsTool.Handler())

	metaTool := tools.NewMetaTool(ranks)
	mcp.AddTool(server, metaTool.Tool(), metaTool.Handler())

	teamAnalysisTool := tools.NewTeamAnalysisTool(gamemasterMgr, ranks)
	mcp.AddTool(server, teamAnalysisTool.Tool(), teamAnalysisTool.Handler())

	teamBuilderTool := tools.NewTeamBuilderTool(gamemasterMgr, ranks)
	mcp.AddTool(server, teamBuilderTool.Tool(), teamBuilderTool.Handler())

	speciesInfoTool := tools.NewSpeciesInfoTool(gamemasterMgr, ranks)
	mcp.AddTool(server, speciesInfoTool.Tool(), speciesInfoTool.Handler())

	moveInfoTool := tools.NewMoveInfoTool(gamemasterMgr)
	mcp.AddTool(server, moveInfoTool.Tool(), moveInfoTool.Handler())

	typeMatchupTool := tools.NewTypeMatchupTool()
	mcp.AddTool(server, typeMatchupTool.Tool(), typeMatchupTool.Handler())

	levelFromCPTool := tools.NewLevelFromCPTool(gamemasterMgr)
	mcp.AddTool(server, levelFromCPTool.Tool(), levelFromCPTool.Handler())

	return server
}

// buildRankingsManager constructs the shared rankings manager using
// the configured cache directory (sibling to gamemaster cache).
func buildRankingsManager(cfg rankings.Config) (*rankings.Manager, error) {
	manager, err := rankings.NewManager(cfg)
	if err != nil {
		return nil, fmt.Errorf("rankings manager: %w", err)
	}

	return manager, nil
}

// rankingsCacheDir derives the per-process rankings cache directory
// from the gamemaster.local_path — both caches share a parent.
func rankingsCacheDir(gamemasterLocalPath string) string {
	return filepath.Join(filepath.Dir(gamemasterLocalPath), rankingsSubdir)
}

// startDebugServer spins up the debug HTTP surface when the
// configured port is non-zero. Returns a channel that closes once the
// server has shut down (either cleanly after ctx cancellation or due
// to a Serve error — so a failed listen does not leak a goroutine
// blocked on ctx.Done). Returns nil when the server is disabled so
// the caller can skip its wait step.
func startDebugServer(
	ctx context.Context, rt *Runtime, mgr *gamemaster.Manager,
) <-chan struct{} {
	if rt.Config.Server.HTTPPort == 0 {
		return nil
	}

	addr := net.JoinHostPort(rt.Config.Server.HTTPHost, strconv.Itoa(rt.Config.Server.HTTPPort))
	server := &http.Server{
		Addr:              addr,
		Handler:           debug.NewHandler(mgr),
		ReadHeaderTimeout: debugServerReadHeaderTimeout,
	}

	// serverDone signals that ListenAndServe has returned. The
	// shutdown-watcher uses it to bail when the server died before
	// ctx cancellation — otherwise the watcher would block on
	// ctx.Done indefinitely, leaking a goroutine.
	serverDone := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(serverDone)

		rt.Logger.Info("starting debug HTTP server", slog.String("addr", addr))

		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			rt.Logger.Error("debug HTTP server failed",
				slog.String("error", err.Error()))
		}
	}()

	go func() {
		defer close(done)

		watchAndShutdown(ctx, rt, server, serverDone)
	}()

	return done
}

// watchAndShutdown blocks until either the parent context is
// cancelled or ListenAndServe returns on its own, then runs
// http.Server.Shutdown on a fresh context so in-flight requests can
// drain. The serverDone channel guards against a goroutine leak: if
// the listener fails immediately (e.g. EADDRINUSE), this function
// still unblocks and closes cleanly without waiting for ctx.Done.
//
//nolint:contextcheck // fresh ctx is required; parent is already cancelled by the time we run
func watchAndShutdown(
	ctx context.Context, rt *Runtime, server *http.Server, serverDone <-chan struct{},
) {
	select {
	case <-ctx.Done():
	case <-serverDone:
		// Server exited on its own; still call Shutdown to release
		// any resources it may have partially acquired.
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), debugServerShutdownGrace)
	defer cancel()

	err := server.Shutdown(shutdownCtx)
	if err != nil {
		rt.Logger.Warn("debug HTTP server shutdown error",
			slog.String("error", err.Error()))
	}
}
