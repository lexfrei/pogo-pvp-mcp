package tools_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

const encounterFixtureGamemaster = `{
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
  ]
}`

func newEncounterCPRangeTool(t *testing.T) *tools.EncounterCPRangeTool {
	t.Helper()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(encounterFixtureGamemaster))
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

	return tools.NewEncounterCPRangeTool(gmMgr)
}

// TestEncounterCPRange_FullTable pins the full-table shape: every
// canonical encounter type appears once with positive CP bounds.
func TestEncounterCPRange_FullTable(t *testing.T) {
	t.Parallel()

	tool := newEncounterCPRangeTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EncounterCPRangeParams{
		Species: speciesMedicham,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Ranges) != 8 {
		t.Fatalf("Ranges len = %d, want 8 (canonical encounter types)", len(result.Ranges))
	}

	seen := make(map[string]bool, len(result.Ranges))
	for _, r := range result.Ranges {
		seen[r.EncounterType] = true

		if r.MinCP <= 0 || r.MaxCP <= 0 {
			t.Errorf("encounter %q: MinCP=%d MaxCP=%d, want positive", r.EncounterType, r.MinCP, r.MaxCP)
		}

		if r.MinCP > r.MaxCP {
			t.Errorf("encounter %q: MinCP=%d > MaxCP=%d", r.EncounterType, r.MinCP, r.MaxCP)
		}
	}

	for _, name := range []string{
		"wild_unboosted", "wild_boosted", "research", "raid", "raid_boosted",
		"gbl_reward", "hatch_egg", "rocket_shadow",
	} {
		if !seen[name] {
			t.Errorf("missing encounter type %q from Ranges", name)
		}
	}
}

// TestEncounterCPRange_SingleType pins the single-query path.
func TestEncounterCPRange_SingleType(t *testing.T) {
	t.Parallel()

	tool := newEncounterCPRangeTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EncounterCPRangeParams{
		Species:       speciesMedicham,
		EncounterType: "raid",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Ranges) != 1 {
		t.Fatalf("Ranges len = %d, want 1", len(result.Ranges))
	}

	raid := result.Ranges[0]
	if raid.EncounterType != "raid" {
		t.Errorf("EncounterType = %q, want \"raid\"", raid.EncounterType)
	}

	if raid.MinLevel != 20 || raid.MaxLevel != 20 {
		t.Errorf("raid level range = (%f, %f), want (20, 20)", raid.MinLevel, raid.MaxLevel)
	}

	if raid.MinIV != 10 {
		t.Errorf("raid MinIV = %d, want 10", raid.MinIV)
	}
}

// TestEncounterCPRange_CaseInsensitive pins that mixed-case
// encounter-type queries resolve identically to lowercase.
func TestEncounterCPRange_CaseInsensitive(t *testing.T) {
	t.Parallel()

	tool := newEncounterCPRangeTool(t)
	handler := tool.Handler()

	_, mixed, err := handler(t.Context(), nil, tools.EncounterCPRangeParams{
		Species: speciesMedicham, EncounterType: "Raid",
	})
	if err != nil {
		t.Fatalf("mixed-case handler: %v", err)
	}

	_, lower, err := handler(t.Context(), nil, tools.EncounterCPRangeParams{
		Species: speciesMedicham, EncounterType: "raid",
	})
	if err != nil {
		t.Fatalf("lower-case handler: %v", err)
	}

	if mixed.Ranges[0].MinCP != lower.Ranges[0].MinCP {
		t.Errorf("case-folded MinCP mismatch: mixed=%d lower=%d",
			mixed.Ranges[0].MinCP, lower.Ranges[0].MinCP)
	}
}

// TestEncounterCPRange_UnknownEncounterType rejects unknown queries.
func TestEncounterCPRange_UnknownEncounterType(t *testing.T) {
	t.Parallel()

	tool := newEncounterCPRangeTool(t)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.EncounterCPRangeParams{
		Species: speciesMedicham, EncounterType: "teraraid",
	})
	if !errors.Is(err, tools.ErrUnknownEncounterType) {
		t.Errorf("error = %v, want wrapping ErrUnknownEncounterType", err)
	}
}

// TestEncounterCPRange_UnknownSpecies rejects unknown species ids.
func TestEncounterCPRange_UnknownSpecies(t *testing.T) {
	t.Parallel()

	tool := newEncounterCPRangeTool(t)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.EncounterCPRangeParams{Species: "missingno"})
	if !errors.Is(err, tools.ErrUnknownSpecies) {
		t.Errorf("error = %v, want wrapping ErrUnknownSpecies", err)
	}
}

// TestEncounterCPRange_RaidBoostedIsHigherThanRaid pins the
// weather-boosted raid mechanic: a raid boss caught in boosting
// weather lands at level 25 instead of the default 20, producing
// strictly higher MaxCP for any species with monotone-positive
// base stats.
func TestEncounterCPRange_RaidBoostedIsHigherThanRaid(t *testing.T) {
	t.Parallel()

	tool := newEncounterCPRangeTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EncounterCPRangeParams{
		Species: speciesMedicham,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	rangesByType := make(map[string]tools.EncounterCPRange, len(result.Ranges))
	for _, r := range result.Ranges {
		rangesByType[r.EncounterType] = r
	}

	raid := rangesByType["raid"]
	raidBoosted := rangesByType["raid_boosted"]

	if raidBoosted.MaxCP <= raid.MaxCP {
		t.Errorf("raid_boosted MaxCP %d ≤ raid MaxCP %d, want strictly greater",
			raidBoosted.MaxCP, raid.MaxCP)
	}

	if raid.MinLevel != 20 || raidBoosted.MinLevel != 25 {
		t.Errorf("level pins: raid=%f raid_boosted=%f, want 20 / 25",
			raid.MinLevel, raidBoosted.MinLevel)
	}
}

// TestEncounterCPRange_GamemasterNotLoaded pins the cold-start
// sentinel the same way every other gamemaster-dependent tool does.
func TestEncounterCPRange_GamemasterNotLoaded(t *testing.T) {
	t.Parallel()

	gmMgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    "http://127.0.0.1:1",
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	tool := tools.NewEncounterCPRangeTool(gmMgr)
	handler := tool.Handler()

	_, _, err = handler(t.Context(), nil, tools.EncounterCPRangeParams{
		Species: speciesMedicham,
	})
	if !errors.Is(err, tools.ErrGamemasterNotLoaded) {
		t.Errorf("error = %v, want wrapping ErrGamemasterNotLoaded", err)
	}
}

// TestEncounterCPRange_EmptySpeciesRejected rejects an empty species
// id up-front rather than producing a confusing "not found" message.
func TestEncounterCPRange_EmptySpeciesRejected(t *testing.T) {
	t.Parallel()

	tool := newEncounterCPRangeTool(t)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.EncounterCPRangeParams{Species: ""})
	if !errors.Is(err, tools.ErrUnknownSpecies) {
		t.Errorf("error = %v, want wrapping ErrUnknownSpecies", err)
	}
}

// TestEncounterCPRange_HatchEggCoversAllTiers documents the
// single-rule design: every egg tier (2km / 5km / 7km / 10km /
// 12km) shares the same level + IV floor, so one `hatch_egg`
// entry covers all of them.
func TestEncounterCPRange_HatchEggCoversAllTiers(t *testing.T) {
	t.Parallel()

	tool := newEncounterCPRangeTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EncounterCPRangeParams{
		Species: speciesMedicham, EncounterType: "hatch_egg",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Ranges) != 1 {
		t.Fatalf("Ranges len = %d, want 1", len(result.Ranges))
	}

	hatch := result.Ranges[0]
	if hatch.MinLevel != 20 || hatch.MaxLevel != 20 || hatch.MinIV != 10 {
		t.Errorf("hatch rule = (level %f-%f, IV floor %d), want (20, 20, 10)",
			hatch.MinLevel, hatch.MaxLevel, hatch.MinIV)
	}
}

// TestEncounterCPRange_WildBoostedIsHigherThanUnboosted pins the
// mechanical invariant: the weather-boosted wild spawn range has a
// strictly higher MaxCP than the unboosted range for every species
// (boosted adds +5 levels, which strictly increases CP for CPM-
// monotone stats).
func TestEncounterCPRange_WildBoostedIsHigherThanUnboosted(t *testing.T) {
	t.Parallel()

	tool := newEncounterCPRangeTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EncounterCPRangeParams{
		Species: speciesMedicham,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	rangesByType := make(map[string]tools.EncounterCPRange, len(result.Ranges))
	for _, r := range result.Ranges {
		rangesByType[r.EncounterType] = r
	}

	unboosted := rangesByType["wild_unboosted"]
	boosted := rangesByType["wild_boosted"]

	if boosted.MaxCP <= unboosted.MaxCP {
		t.Errorf("boosted MaxCP %d ≤ unboosted MaxCP %d, want strictly greater",
			boosted.MaxCP, unboosted.MaxCP)
	}
}
