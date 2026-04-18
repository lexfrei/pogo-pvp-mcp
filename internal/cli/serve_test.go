package cli

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
)

// newServeTestRuntime builds a minimal Runtime pointing the
// gamemaster at a tmpdir path. The managers are initialised but not
// primed, which is enough for the debug-server tests that don't
// touch the snapshot.
func newServeTestRuntime(t *testing.T, httpPort int) *Runtime {
	t.Helper()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Transport: "stdio",
			HTTPHost:  "127.0.0.1",
			HTTPPort:  httpPort,
		},
		Log: config.LogConfig{Level: "info", Format: "text"},
		Gamemaster: config.GamemasterConfig{
			Source:          "http://example.invalid",
			LocalPath:       filepath.Join(t.TempDir(), "gm.json"),
			RefreshInterval: time.Hour,
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	return &Runtime{Config: cfg, Logger: logger, Stdout: &buf, Stderr: &buf}
}

func newServeTestManager(t *testing.T, cfg config.GamemasterConfig) *gamemaster.Manager {
	t.Helper()

	mgr, err := gamemaster.NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	return mgr
}

// TestStartDebugServer_DisabledReturnsNil pins the opt-out path: with
// HTTPPort=0 the helper must return a nil channel so the caller's
// wait step skips cleanly.
func TestStartDebugServer_DisabledReturnsNil(t *testing.T) {
	t.Parallel()

	rt := newServeTestRuntime(t, 0)
	mgr := newServeTestManager(t, rt.Config.Gamemaster)

	done := startDebugServer(t.Context(), rt, mgr)
	if done != nil {
		t.Error("startDebugServer returned non-nil channel for HTTPPort=0")
	}
}

// TestStartDebugServer_ShutsDownOnContextCancel verifies the happy-
// path lifecycle: server binds, serves, and exits within the grace
// window when the parent ctx is cancelled.
func TestStartDebugServer_ShutsDownOnContextCancel(t *testing.T) {
	t.Parallel()

	port := pickFreePort(t)
	rt := newServeTestRuntime(t, port)
	mgr := newServeTestManager(t, rt.Config.Gamemaster)

	ctx, cancel := context.WithCancel(t.Context())
	done := startDebugServer(ctx, rt, mgr)

	waitForPortReady(t, port)

	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("startDebugServer did not close done within 3s of ctx cancel")
	}
}

// TestStartDebugServer_DoesNotLeakOnListenFailure confirms the fix
// for the goroutine leak when ListenAndServe fails before ctx
// cancellation. A second startDebugServer on the same port triggers
// EADDRINUSE; its done channel must still close promptly.
func TestStartDebugServer_DoesNotLeakOnListenFailure(t *testing.T) {
	t.Parallel()

	port := pickFreePort(t)

	// Hold the port so the next ListenAndServe fails immediately.
	var lc net.ListenConfig

	blocker, err := lc.Listen(t.Context(), "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("blocker listen: %v", err)
	}
	defer blocker.Close()

	rt := newServeTestRuntime(t, port)
	mgr := newServeTestManager(t, rt.Config.Gamemaster)

	done := startDebugServer(t.Context(), rt, mgr)

	// done must close once ListenAndServe errors, without waiting on
	// the parent ctx. Grace window covers scheduler jitter.
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("done did not close within 3s after ListenAndServe error — goroutine leak?")
	}
}

// TestWaitForBackgroundShutdown_NilDebugReturnsAfterRefresh checks
// the refresh-only path: when the debug server was never started,
// wait must not hang on a nil channel.
func TestWaitForBackgroundShutdown_NilDebugReturnsAfterRefresh(t *testing.T) {
	t.Parallel()

	rt := newServeTestRuntime(t, 0)
	refreshDone := make(chan struct{})
	close(refreshDone)

	doneCh := make(chan struct{})

	go func() {
		waitForBackgroundShutdown(rt, refreshDone, nil)
		close(doneCh)
	}()

	select {
	case <-doneCh:
	case <-time.After(time.Second):
		t.Fatal("waitForBackgroundShutdown hung on nil debug channel")
	}
}

// TestBuildManagers_Smoke verifies buildManagers wires both managers
// up without panicking when fed a valid config. Actual manager
// behaviour is covered in the gamemaster/rankings package tests.
func TestBuildManagers_Smoke(t *testing.T) {
	t.Parallel()

	rt := newServeTestRuntime(t, 0)

	bundle, err := buildManagers(rt)
	if err != nil {
		t.Fatalf("buildManagers: %v", err)
	}
	if bundle.Gamemaster == nil {
		t.Error("Gamemaster manager is nil")
	}
	if bundle.Rankings == nil {
		t.Error("Rankings manager is nil")
	}
}

// pickFreePort asks the kernel for an unused TCP port and returns
// it after closing the test listener. Not race-free (another process
// could grab the port between Close and ListenAndServe), but fine
// for our single-process test suite.
func pickFreePort(t *testing.T) int {
	t.Helper()

	var lc net.ListenConfig

	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		t.Fatalf("listener addr is not *net.TCPAddr: %T", listener.Addr())
	}

	port := addr.Port

	err = listener.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	return port
}

// waitForPortReady polls the debug endpoint until the server starts
// accepting connections or the deadline elapses.
func waitForPortReady(t *testing.T, port int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)

	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()

			return
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("port %d never became ready", port)
}
