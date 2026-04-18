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

const speciesMedicham = "medicham"

const cplimitsFixtureGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-18 00:00:00",
  "pokemon": [
    {"dex": 308, "speciesId": "medicham", "speciesName": "Medicham",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"],
     "released": true},
    {"dex": 149, "speciesId": "dragonite", "speciesName": "Dragonite",
     "baseStats": {"atk": 263, "def": 198, "hp": 209},
     "types": ["dragon", "flying"],
     "fastMoves": ["DRAGON_TAIL"], "chargedMoves": ["OUTRAGE"],
     "released": true}
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "energyGain": 0, "cooldown": 500},
    {"moveId": "DRAGON_TAIL", "name": "Dragon Tail", "type": "dragon",
     "power": 13, "energy": 0, "energyGain": 9, "cooldown": 1500, "turns": 3},
    {"moveId": "OUTRAGE", "name": "Outrage", "type": "dragon",
     "power": 110, "energy": 50, "energyGain": 0, "cooldown": 500}
  ]
}`

func newCPLimitsTestManager(t *testing.T) *gamemaster.Manager {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(cplimitsFixtureGamemaster))
	}))
	t.Cleanup(server.Close)

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    server.URL,
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	err = mgr.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	return mgr
}

func TestCPLimitsTool_MedichamThreeLeagues(t *testing.T) {
	t.Parallel()

	mgr := newCPLimitsTestManager(t)
	handler := tools.NewCPLimitsTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.CPLimitsParams{
		Species: speciesMedicham,
		IV:      [3]int{15, 15, 15},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Species != speciesMedicham {
		t.Errorf("Species = %q, want %q", result.Species, speciesMedicham)
	}
	if len(result.Leagues) != 3 {
		t.Fatalf("Leagues len = %d, want 3 (little, great, ultra)", len(result.Leagues))
	}

	want := map[string]int{
		"little": 500,
		"great":  1500,
		"ultra":  2500,
	}

	for _, entry := range result.Leagues {
		expectedCap, ok := want[entry.League]
		if !ok {
			t.Errorf("unexpected league %q", entry.League)
			continue
		}
		if entry.CPCap != expectedCap {
			t.Errorf("league %s CPCap = %d, want %d", entry.League, entry.CPCap, expectedCap)
		}
		if entry.MaxCP <= 0 {
			t.Errorf("league %s MaxCP = %d, want > 0", entry.League, entry.MaxCP)
		}
		if entry.MaxCP > expectedCap {
			t.Errorf("league %s MaxCP = %d exceeds cap %d", entry.League, entry.MaxCP, expectedCap)
		}
		if entry.MaxLevel < 1.0 || entry.MaxLevel > 51.0 {
			t.Errorf("league %s MaxLevel = %f outside [1, 51]", entry.League, entry.MaxLevel)
		}
	}
}

// TestCPLimitsTool_MonotonicAcrossCaps pins the obvious invariant:
// a bigger cap cannot yield a smaller max_cp or a lower max_level
// (the same IV walking the same level grid can only go higher).
func TestCPLimitsTool_MonotonicAcrossCaps(t *testing.T) {
	t.Parallel()

	mgr := newCPLimitsTestManager(t)
	handler := tools.NewCPLimitsTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.CPLimitsParams{
		Species: speciesMedicham,
		IV:      [3]int{15, 15, 15},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	byLeague := map[string]tools.LeagueCPLimit{}
	for _, entry := range result.Leagues {
		byLeague[entry.League] = entry
	}

	if byLeague["great"].MaxCP <= byLeague["little"].MaxCP {
		t.Errorf("great MaxCP (%d) should exceed little (%d)",
			byLeague["great"].MaxCP, byLeague["little"].MaxCP)
	}
	if byLeague["ultra"].MaxCP <= byLeague["great"].MaxCP {
		t.Errorf("ultra MaxCP (%d) should exceed great (%d)",
			byLeague["ultra"].MaxCP, byLeague["great"].MaxCP)
	}

	if byLeague["great"].MaxLevel < byLeague["little"].MaxLevel {
		t.Errorf("great MaxLevel regressed vs little: %f < %f",
			byLeague["great"].MaxLevel, byLeague["little"].MaxLevel)
	}
}

// TestCPLimitsTool_DragoniteHigherLevelInUltra verifies that even a
// heavyweight like Dragonite fits in every standard league — the CP
// 10 floor guarantees level-1 CP ≥ 10, which is always under 500.
// Ultra's max_level still must be higher than Great's for the same
// IVs, because more headroom ⇒ higher reachable level.
func TestCPLimitsTool_DragoniteHigherLevelInUltra(t *testing.T) {
	t.Parallel()

	mgr := newCPLimitsTestManager(t)
	handler := tools.NewCPLimitsTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.CPLimitsParams{
		Species: "dragonite",
		IV:      [3]int{15, 15, 15},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	byLeague := map[string]tools.LeagueCPLimit{}
	for _, entry := range result.Leagues {
		byLeague[entry.League] = entry
	}

	for _, entry := range result.Leagues {
		if !entry.Fits {
			t.Errorf("league %s: Fits=false for dragonite — level 1 CP should still clear %d",
				entry.League, entry.CPCap)
		}
	}

	if byLeague["ultra"].MaxLevel <= byLeague["great"].MaxLevel {
		t.Errorf("dragonite ultra.MaxLevel (%.1f) should exceed great (%.1f)",
			byLeague["ultra"].MaxLevel, byLeague["great"].MaxLevel)
	}
}

func TestCPLimitsTool_UnknownSpecies(t *testing.T) {
	t.Parallel()

	mgr := newCPLimitsTestManager(t)
	handler := tools.NewCPLimitsTool(mgr).Handler()

	_, _, err := handler(t.Context(), nil, tools.CPLimitsParams{
		Species: "missingno",
		IV:      [3]int{15, 15, 15},
	})
	if !errors.Is(err, tools.ErrUnknownSpecies) {
		t.Errorf("error = %v, want wrapping ErrUnknownSpecies", err)
	}
}

func TestCPLimitsTool_InvalidIV(t *testing.T) {
	t.Parallel()

	mgr := newCPLimitsTestManager(t)
	handler := tools.NewCPLimitsTool(mgr).Handler()

	_, _, err := handler(t.Context(), nil, tools.CPLimitsParams{
		Species: speciesMedicham,
		IV:      [3]int{16, 0, 0},
	})
	if err == nil {
		t.Fatal("expected error for out-of-range IV")
	}
}

func TestCPLimitsTool_NoGamemasterLoaded(t *testing.T) {
	t.Parallel()

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    "http://example.invalid",
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	handler := tools.NewCPLimitsTool(mgr).Handler()

	_, _, err = handler(t.Context(), nil, tools.CPLimitsParams{
		Species: speciesMedicham,
		IV:      [3]int{15, 15, 15},
	})
	if !errors.Is(err, tools.ErrGamemasterNotLoaded) {
		t.Errorf("error = %v, want wrapping ErrGamemasterNotLoaded", err)
	}
}
