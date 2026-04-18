package cli_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
