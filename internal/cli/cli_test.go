package cli_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lexfrei/pogo-pvp-mcp/internal/cli"
)

const cliFixtureGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-18 00:00:00",
  "pokemon": [
    {
      "dex": 1,
      "speciesId": "bulbasaur",
      "speciesName": "Bulbasaur",
      "baseStats": {"atk": 118, "def": 111, "hp": 128},
      "types": ["grass", "poison"],
      "fastMoves": ["VINE_WHIP"],
      "chargedMoves": ["SLUDGE_BOMB"],
      "released": true
    }
  ],
  "moves": [
    {"moveId": "VINE_WHIP", "name": "Vine Whip", "type": "grass", "power": 5, "energy": 0, "energyGain": 8, "cooldown": 1000, "turns": 2},
    {"moveId": "SLUDGE_BOMB", "name": "Sludge Bomb", "type": "poison", "power": 80, "energy": 50, "energyGain": 0, "cooldown": 500}
  ]
}`

// TestNewRootCommand_FetchGMHappyPath runs the fetch-gm subcommand
// end-to-end via the cobra tree against an injected upstream HTTP
// server, proving the zero-config path works: the default
// gamemaster.local_path is derived, the file is written, and the
// command exits 0.
func TestNewRootCommand_FetchGMHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(cliFixtureGamemaster))
	}))
	defer server.Close()

	cachePath := filepath.Join(t.TempDir(), "gamemaster.json")

	t.Setenv("POGO_PVP_GAMEMASTER_SOURCE", server.URL)
	t.Setenv("POGO_PVP_GAMEMASTER_LOCAL_PATH", cachePath)

	var stdout, stderr bytes.Buffer

	root := cli.NewRootCommand(&stdout, &stderr)
	root.SetArgs([]string{"fetch-gm"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("Execute: %v (stderr=%s)", err, stderr.String())
	}

	info, err := os.Stat(cachePath)
	if err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	if info.Size() == 0 {
		t.Error("cache file is empty")
	}
}

// TestNewRootCommand_FetchGMUpstreamError surfaces upstream failures
// as non-nil Execute errors.
func TestNewRootCommand_FetchGMUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	t.Setenv("POGO_PVP_GAMEMASTER_SOURCE", server.URL)
	t.Setenv("POGO_PVP_GAMEMASTER_LOCAL_PATH", filepath.Join(t.TempDir(), "gm.json"))

	var stdout, stderr bytes.Buffer

	root := cli.NewRootCommand(&stdout, &stderr)
	root.SetArgs([]string{"fetch-gm"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error on upstream 500, got nil")
	}
}

// TestNewRootCommand_NonexistentConfigFlag verifies that a bad
// --config path surfaces as a command error rather than silently
// falling back to defaults.
func TestNewRootCommand_NonexistentConfigFlag(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	root := cli.NewRootCommand(&stdout, &stderr)
	root.SetArgs([]string{"--config", "/definitely/not/here.yaml", "fetch-gm"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent --config path")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %v, want to contain 'load config'", err)
	}
}

// TestNewRootCommand_ConfigEnvDefault exercises the POGO_PVP_CONFIG
// fallback: the env var supplies the default for --config, so the
// user does not have to repeat the flag on every invocation.
func TestNewRootCommand_ConfigEnvDefault(t *testing.T) {
	t.Setenv("POGO_PVP_CONFIG", "/definitely/not/here.yaml")

	var stdout, stderr bytes.Buffer

	root := cli.NewRootCommand(&stdout, &stderr)
	root.SetArgs([]string{"fetch-gm"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent POGO_PVP_CONFIG path")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error = %v, want to contain 'load config' (env path should have been honoured)", err)
	}
}

// TestNewRootCommand_DiffGMNoChanges exercises the diff-gm subcommand
// end-to-end when upstream and local cache hold the same payload:
// the command exits 0 and prints the "no changes" line.
func TestNewRootCommand_DiffGMNoChanges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(cliFixtureGamemaster))
	}))
	defer server.Close()

	cachePath := filepath.Join(t.TempDir(), "gamemaster.json")

	err := os.WriteFile(cachePath, []byte(cliFixtureGamemaster), 0o600)
	if err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	t.Setenv("POGO_PVP_GAMEMASTER_SOURCE", server.URL)
	t.Setenv("POGO_PVP_GAMEMASTER_LOCAL_PATH", cachePath)

	var stdout, stderr bytes.Buffer

	root := cli.NewRootCommand(&stdout, &stderr)
	root.SetArgs([]string{"diff-gm"})

	err = root.Execute()
	if err != nil {
		t.Fatalf("Execute: %v (stderr=%s)", err, stderr.String())
	}

	if !strings.Contains(stdout.String(), "no changes") {
		t.Errorf("stdout = %q, want \"no changes\" line", stdout.String())
	}
}

// TestNewRootCommand_DiffGMDetectsDrift seeds an old gamemaster on
// disk, serves a mutated one upstream, and asserts the command prints
// the drift summary and exits via ErrDiffDirty.
func TestNewRootCommand_DiffGMDetectsDrift(t *testing.T) {
	const mutatedGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {
      "dex": 1,
      "speciesId": "bulbasaur",
      "speciesName": "Bulbasaur",
      "baseStats": {"atk": 125, "def": 111, "hp": 128},
      "types": ["grass", "poison"],
      "fastMoves": ["VINE_WHIP"],
      "chargedMoves": ["SLUDGE_BOMB"],
      "released": true
    }
  ],
  "moves": [
    {"moveId": "VINE_WHIP", "name": "Vine Whip", "type": "grass", "power": 5, "energy": 0, "energyGain": 8, "cooldown": 1000, "turns": 2},
    {"moveId": "SLUDGE_BOMB", "name": "Sludge Bomb", "type": "poison", "power": 80, "energy": 50, "energyGain": 0, "cooldown": 500}
  ]
}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(mutatedGamemaster))
	}))
	defer server.Close()

	cachePath := filepath.Join(t.TempDir(), "gamemaster.json")

	err := os.WriteFile(cachePath, []byte(cliFixtureGamemaster), 0o600)
	if err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	t.Setenv("POGO_PVP_GAMEMASTER_SOURCE", server.URL)
	t.Setenv("POGO_PVP_GAMEMASTER_LOCAL_PATH", cachePath)

	var stdout, stderr bytes.Buffer

	root := cli.NewRootCommand(&stdout, &stderr)
	root.SetArgs([]string{"diff-gm"})

	err = root.Execute()
	if !errors.Is(err, cli.ErrDiffDirty) {
		t.Fatalf("Execute = %v, want wrapping ErrDiffDirty", err)
	}

	if !strings.Contains(stdout.String(), "bulbasaur") {
		t.Errorf("stdout = %q, want to mention the changed species", stdout.String())
	}

	// diff-gm is read-only — the cache must still hold the seeded
	// content after the command runs, not the mutated upstream.
	cached, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if !strings.Contains(string(cached), `"atk": 118`) {
		t.Error("diff-gm mutated the cache; Phase F.4 invariant broken")
	}
}

// TestNewRootCommand_DiffGMAgainstMissingFileErrors pins the round-2
// review fix: `diff-gm --against /typo/path` must surface the
// file-not-found error rather than silently treating the missing
// file as an empty baseline (which would print every local species
// as "removed" and exit via ErrDiffDirty, masquerading as a
// catastrophic upstream purge).
func TestNewRootCommand_DiffGMAgainstMissingFileErrors(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "gamemaster.json")

	err := os.WriteFile(cachePath, []byte(cliFixtureGamemaster), 0o600)
	if err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	// POGO_PVP_GAMEMASTER_SOURCE is required by config validation
	// even though the --against branch never touches the network.
	t.Setenv("POGO_PVP_GAMEMASTER_SOURCE", "https://example.invalid")
	t.Setenv("POGO_PVP_GAMEMASTER_LOCAL_PATH", cachePath)

	var stdout, stderr bytes.Buffer

	root := cli.NewRootCommand(&stdout, &stderr)
	root.SetArgs([]string{"diff-gm", "--against", "/definitely/not/here.json"})

	err = root.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want a file-not-found error")
	}

	if errors.Is(err, cli.ErrDiffDirty) {
		t.Errorf("Execute wrapped ErrDiffDirty, want a distinct missing-file error — "+
			"regression: --against treated missing files as empty baseline. err=%v", err)
	}

	if !errors.Is(err, os.ErrNotExist) {
		lowered := strings.ToLower(err.Error())
		if !strings.Contains(lowered, "no such file") &&
			!strings.Contains(lowered, "not exist") {
			t.Errorf("Execute = %v, want to wrap os.ErrNotExist (or contain 'not exist')", err)
		}
	}
}

// TestNewRootCommand_DiffGMRespectsContextDeadline proves diff-gm
// does not hang on a slow / unresponsive upstream: the HTTP request
// is built with NewRequestWithContext, so a context deadline from
// the caller (cron wrapper, cobra) aborts the fetch promptly. A
// regression that drops ctx threading — or that swallows the cancel
// error into a silent retry — fails this test by exceeding the
// 5-second hard cap. The client's own Timeout field (30s production
// default) is a separate defense-in-depth guard not exercised here
// because it would require a 30-second test.
func TestNewRootCommand_DiffGMRespectsContextDeadline(t *testing.T) {
	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	cachePath := filepath.Join(t.TempDir(), "gamemaster.json")

	err := os.WriteFile(cachePath, []byte(cliFixtureGamemaster), 0o600)
	if err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	t.Setenv("POGO_PVP_GAMEMASTER_SOURCE", server.URL)
	t.Setenv("POGO_PVP_GAMEMASTER_LOCAL_PATH", cachePath)

	var stdout, stderr bytes.Buffer

	root := cli.NewRootCommand(&stdout, &stderr)
	root.SetArgs([]string{"diff-gm"})

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)

	go func() {
		done <- root.ExecuteContext(ctx)
	}()

	select {
	case runErr := <-done:
		if runErr == nil {
			t.Fatal("Execute returned nil, want a timeout/cancel error")
		}

		lowered := strings.ToLower(runErr.Error())
		if !strings.Contains(lowered, "deadline") &&
			!strings.Contains(lowered, "canceled") &&
			!strings.Contains(lowered, "cancelled") &&
			!strings.Contains(lowered, "timeout") {
			t.Errorf("Execute = %v, want a deadline/canceled/timeout error", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("diff-gm ignored its ctx deadline — regression: request is not context-aware")
	}
}

// TestNewRootCommand_HelpDoesNotRequireConfig verifies that invoking
// --help does not trigger the PersistentPreRunE config load, so a
// broken config file cannot prevent users from seeing usage.
func TestNewRootCommand_HelpDoesNotRequireConfig(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	root := cli.NewRootCommand(&stdout, &stderr)
	root.SetArgs([]string{"--help"})

	err := root.Execute()
	if err != nil {
		t.Errorf("--help returned error: %v", err)
	}
}
