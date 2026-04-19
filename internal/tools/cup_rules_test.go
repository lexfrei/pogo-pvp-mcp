package tools_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

const (
	// cupAllLabel is the normalised cup id used when no cup is
	// specified — resolveCupLabel maps "" → "all" for display purposes.
	// Shared with the sibling tool tests that exercise the same label.
	cupAllLabel = "all"
	// cupSpringLabel is the pvpoke id for the Spring Cup, exercised
	// by several cup_rules tests.
	cupSpringLabel = "spring"
)

const cupRulesFixtureGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 308, "speciesId": "medicham", "speciesName": "Medicham",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"], "released": true}
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "cooldown": 500}
  ],
  "cups": [
    {"name": "all", "title": "All Pokemon",
     "include": [],
     "exclude": [{"filterType": "tag", "values": ["mega"]}]},
    {"name": "spring", "title": "Spring Cup",
     "include": [{"filterType": "type", "values": ["water", "grass", "fairy"]}],
     "exclude": [
       {"filterType": "tag", "values": ["mega"]},
       {"filterType": "id", "values": ["jumpluff"]}
     ],
     "partySize": 3}
  ]
}`

func newCupRulesTool(t *testing.T) *tools.CupRulesTool {
	t.Helper()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(cupRulesFixtureGamemaster))
	}))
	t.Cleanup(gmServer.Close)

	gmMgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    gmServer.URL,
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager gm: %v", err)
	}

	err = gmMgr.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh gm: %v", err)
	}

	return tools.NewCupRulesTool(gmMgr)
}

// TestCupRules_FullTable pins the empty-query path: returns every
// cup in the snapshot, sorted by cup id.
func TestCupRules_FullTable(t *testing.T) {
	t.Parallel()

	tool := newCupRulesTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.CupRulesParams{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Entries) != 2 {
		t.Fatalf("Entries len = %d, want 2 (all + spring)", len(result.Entries))
	}

	// Sort invariant: ascending cup id.
	if result.Entries[0].Cup != cupAllLabel || result.Entries[1].Cup != cupSpringLabel {
		t.Errorf("Entries order = %q, %q; want %q, %q",
			result.Entries[0].Cup, result.Entries[1].Cup,
			cupAllLabel, cupSpringLabel)
	}
}

// TestCupRules_SingleCup pins the single-query path.
func TestCupRules_SingleCup(t *testing.T) {
	t.Parallel()

	tool := newCupRulesTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.CupRulesParams{Cup: cupSpringLabel})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Query != cupSpringLabel {
		t.Errorf("Query = %q, want %q", result.Query, cupSpringLabel)
	}

	if len(result.Entries) != 1 {
		t.Fatalf("Entries len = %d, want 1", len(result.Entries))
	}

	spring := result.Entries[0]
	if spring.Cup != cupSpringLabel {
		t.Errorf("Cup = %q, want %q", spring.Cup, cupSpringLabel)
	}

	if spring.PartySize != 3 {
		t.Errorf("PartySize = %d, want 3", spring.PartySize)
	}

	if len(spring.Include) != 1 {
		t.Fatalf("Include len = %d, want 1 (single type filter)", len(spring.Include))
	}

	wantTypes := []string{"water", "grass", "fairy"}
	if !slices.Equal(spring.Include[0].Values, wantTypes) {
		t.Errorf("Include[0].Values = %v, want %v", spring.Include[0].Values, wantTypes)
	}

	if len(spring.Exclude) != 2 {
		t.Errorf("Exclude len = %d, want 2 (tag + id)", len(spring.Exclude))
	}
}

// TestCupRules_UnknownCup rejects cup ids not in the gamemaster.
func TestCupRules_UnknownCup(t *testing.T) {
	t.Parallel()

	tool := newCupRulesTool(t)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.CupRulesParams{Cup: "phantom"})
	if !errors.Is(err, tools.ErrUnknownCupRule) {
		t.Errorf("error = %v, want wrapping ErrUnknownCupRule", err)
	}
}

// TestCupRules_GamemasterNotLoaded pins the cold-start sentinel.
func TestCupRules_GamemasterNotLoaded(t *testing.T) {
	t.Parallel()

	gmMgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    "http://127.0.0.1:1",
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	tool := tools.NewCupRulesTool(gmMgr)
	handler := tool.Handler()

	_, _, err = handler(t.Context(), nil, tools.CupRulesParams{})
	if !errors.Is(err, tools.ErrGamemasterNotLoaded) {
		t.Errorf("error = %v, want wrapping ErrGamemasterNotLoaded", err)
	}
}

// TestCupRules_ResultIsDefensivelyCloned pins that the returned
// filter Values slice is not aliased into the snapshot — mutating
// the response must not corrupt subsequent calls.
func TestCupRules_ResultIsDefensivelyCloned(t *testing.T) {
	t.Parallel()

	tool := newCupRulesTool(t)
	handler := tool.Handler()

	_, first, err := handler(t.Context(), nil, tools.CupRulesParams{Cup: "spring"})
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	first.Entries[0].Include[0].Values[0] = "MUTATED"

	_, second, err := handler(t.Context(), nil, tools.CupRulesParams{Cup: "spring"})
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	if slices.Contains(second.Entries[0].Include[0].Values, "MUTATED") {
		t.Errorf("second call surfaced mutated data — slice is aliased, not cloned")
	}
}
