package tools_test

import (
	"errors"
	"path/filepath"
	"testing"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

// levelFromCPFixture carries medicham's stat line plus dragonite so
// the tests cover both a CP-bound species (medicham easy to hit 1500)
// and a heavyweight (dragonite saturates earlier).
const levelFromCPFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
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
     "power": 55, "energy": 40, "cooldown": 500},
    {"moveId": "DRAGON_TAIL", "name": "Dragon Tail", "type": "dragon",
     "power": 13, "energy": 0, "energyGain": 9, "cooldown": 1500, "turns": 3},
    {"moveId": "OUTRAGE", "name": "Outrage", "type": "dragon",
     "power": 110, "energy": 50, "cooldown": 500}
  ]
}`

// TestLevelFromCP_ReachableWhenTargetFitsUnderCap pins the positive
// case: a CP target above the picked level's CP but strictly below
// the IV spread's max reachable CP must flag Reachable=true. The
// picked level is just the greatest-under-target grid point, not a
// signal of unreachability.
func TestLevelFromCP_ReachableWhenTargetFitsUnderCap(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, levelFromCPFixture)
	handler := tools.NewLevelFromCPTool(mgr).Handler()

	// medicham [15,15,15] tops out at ~1431 CP without XL; 1300 is
	// comfortably below that, so the handler picks a level < cap
	// and Reachable must be true (just nearest-under-target on the
	// 0.5 grid, not an impossibility signal).
	_, result, err := handler(t.Context(), nil, tools.LevelFromCPParams{
		Species: speciesMedicham,
		IV:      [3]int{15, 15, 15},
		CP:      1300,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if !result.Reachable {
		t.Errorf("Reachable = false, want true (CP 1300 sits below medicham's max CP, level < cap)")
	}
	if result.Level >= 40 {
		t.Errorf("Level = %.1f, want below cap 40 (otherwise target isn't actually below max)",
			result.Level)
	}
}

// TestLevelFromCP_UnreachableAtLevelCap pins r7 finding: a CP
// target that the IV spread cannot reach even at the level cap
// must flag Reachable=false so the caller can distinguish "off
// grid, nearest level picked" from "impossible target". Caller
// previously had to compare CP fields to detect this.
func TestLevelFromCP_UnreachableAtLevelCap(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, levelFromCPFixture)
	handler := tools.NewLevelFromCPTool(mgr).Handler()

	// CP 9999 is far beyond any medicham level can reach; without
	// XL the cap is NoXLMaxLevel=40. Handler should clamp at 40
	// and flag Reachable=false.
	_, result, err := handler(t.Context(), nil, tools.LevelFromCPParams{
		Species: speciesMedicham,
		IV:      [3]int{15, 15, 15},
		CP:      9999,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Reachable {
		t.Errorf("Reachable = true, want false (CP 9999 unreachable at level cap 40)")
	}
	if result.Exact {
		t.Errorf("Exact = true, want false (CP 9999 unreachable)")
	}
	if result.CP >= 9999 {
		t.Errorf("CP = %d, want capped below 9999", result.CP)
	}
}

func TestLevelFromCP_FitsUnderCap(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, levelFromCPFixture)
	handler := tools.NewLevelFromCPTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.LevelFromCPParams{
		Species: speciesMedicham,
		IV:      [3]int{0, 15, 15},
		CP:      1500,
		XL:      true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.CP > 1500 {
		t.Errorf("CP = %d, want ≤ 1500", result.CP)
	}
	if result.Level < 1.0 || result.Level > 51.0 {
		t.Errorf("Level = %.1f outside [1.0, 51.0]", result.Level)
	}
	if result.StatProduct <= 0 {
		t.Errorf("StatProduct = %f, want positive", result.StatProduct)
	}
	if result.HP <= 0 {
		t.Errorf("HP = %d, want positive", result.HP)
	}
}

// TestLevelFromCP_ExactRoundTrip builds a known (species, iv, level)
// triple, computes its CP via the engine, feeds that CP back to the
// tool, and asserts Exact=true plus Level recovers the input.
func TestLevelFromCP_ExactRoundTrip(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, levelFromCPFixture)
	handler := tools.NewLevelFromCPTool(mgr).Handler()

	// Compute the medicham-40.0 CP via the engine so the test
	// stays immune to pvpoke stat-table drift.
	const level = 40.0

	ivs, err := pogopvp.NewIV(0, 15, 15)
	if err != nil {
		t.Fatalf("NewIV: %v", err)
	}

	cpm, err := pogopvp.CPMAt(level)
	if err != nil {
		t.Fatalf("CPMAt: %v", err)
	}

	base := pogopvp.BaseStats{Atk: 121, Def: 152, HP: 155}
	targetCP := pogopvp.ComputeCP(base, ivs, cpm)

	_, result, err := handler(t.Context(), nil, tools.LevelFromCPParams{
		Species: speciesMedicham,
		IV:      [3]int{0, 15, 15},
		CP:      targetCP,
		XL:      true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if !result.Exact {
		t.Errorf("Exact = false, want true (round-trip CP must recover exactly)")
	}
	if result.CP != targetCP {
		t.Errorf("CP = %d, want %d", result.CP, targetCP)
	}
	if result.Level < level {
		t.Errorf("Level = %.1f, want ≥ %.1f", result.Level, level)
	}
}

func TestLevelFromCP_UnknownSpecies(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, levelFromCPFixture)
	handler := tools.NewLevelFromCPTool(mgr).Handler()

	_, _, err := handler(t.Context(), nil, tools.LevelFromCPParams{
		Species: "missingno",
		IV:      [3]int{15, 15, 15},
		CP:      1500,
	})
	if !errors.Is(err, tools.ErrUnknownSpecies) {
		t.Errorf("error = %v, want wrapping ErrUnknownSpecies", err)
	}
}

func TestLevelFromCP_InvalidIV(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, levelFromCPFixture)
	handler := tools.NewLevelFromCPTool(mgr).Handler()

	_, _, err := handler(t.Context(), nil, tools.LevelFromCPParams{
		Species: speciesMedicham,
		IV:      [3]int{16, 0, 0},
		CP:      1500,
	})
	if err == nil {
		t.Fatal("expected error for out-of-range IV")
	}
}

// TestLevelFromCP_TooLow pins the engine ErrCPTooLow propagation:
// a target CP below the species' level-1 minimum surfaces the
// engine sentinel unchanged so callers can branch on it.
func TestLevelFromCP_TooLow(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, levelFromCPFixture)
	handler := tools.NewLevelFromCPTool(mgr).Handler()

	_, _, err := handler(t.Context(), nil, tools.LevelFromCPParams{
		Species: "dragonite",
		IV:      [3]int{15, 15, 15},
		CP:      5,
	})
	if !errors.Is(err, pogopvp.ErrCPTooLow) {
		t.Errorf("error = %v, want wrapping pogopvp.ErrCPTooLow", err)
	}
}

func TestLevelFromCP_NoGamemasterLoaded(t *testing.T) {
	t.Parallel()

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    "http://example.invalid",
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	handler := tools.NewLevelFromCPTool(mgr).Handler()

	_, _, err = handler(t.Context(), nil, tools.LevelFromCPParams{
		Species: speciesMedicham,
		IV:      [3]int{15, 15, 15},
		CP:      1500,
	})
	if !errors.Is(err, tools.ErrGamemasterNotLoaded) {
		t.Errorf("error = %v, want wrapping ErrGamemasterNotLoaded", err)
	}
}

// levelFromCPShadowFixture mirrors levelFromCPFixture but also
// publishes "medicham_shadow" as a distinct gamemaster entry so
// Options.Shadow can flip the lookup. Stats are identical (pvpoke
// publishes shadow rows with the same BaseStats; the flag is
// semantic, not stat-multiplicative in the gamemaster).
const levelFromCPShadowFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 308, "speciesId": "medicham", "speciesName": "Medicham",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"],
     "released": true},
    {"dex": 308, "speciesId": "medicham_shadow", "speciesName": "Medicham (Shadow)",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"],
     "released": true}
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "cooldown": 500}
  ]
}`

// TestLevelFromCP_ShadowOptionResolvesToShadowEntry pins Phase X-II:
// Options.Shadow=true must flip the species lookup to the "_shadow"
// pvpoke entry, and the response must echo ResolvedSpeciesID =
// "medicham_shadow" so callers can verify the redirect happened.
func TestLevelFromCP_ShadowOptionResolvesToShadowEntry(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, levelFromCPShadowFixture)
	handler := tools.NewLevelFromCPTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.LevelFromCPParams{
		Species: speciesMedicham,
		IV:      [3]int{15, 15, 15},
		CP:      1500,
		XL:      true,
		Options: tools.CombatantOptions{Shadow: true},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.ResolvedSpeciesID != speciesMedichamShadow {
		t.Errorf("ResolvedSpeciesID = %q, want %q (Options.Shadow must flip to _shadow entry)",
			result.ResolvedSpeciesID, speciesMedichamShadow)
	}

	if result.ShadowVariantMissing {
		t.Errorf("ShadowVariantMissing = true; fixture publishes _shadow entry — must not signal missing")
	}
}

// TestLevelFromCP_ShadowMissingFallback pins the converse path:
// Options.Shadow=true when pvpoke does not publish the shadow row
// falls back to the base species with ShadowVariantMissing=true.
func TestLevelFromCP_ShadowMissingFallback(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, levelFromCPFixture)
	handler := tools.NewLevelFromCPTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.LevelFromCPParams{
		Species: speciesMedicham,
		IV:      [3]int{15, 15, 15},
		CP:      1500,
		XL:      true,
		Options: tools.CombatantOptions{Shadow: true},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.ResolvedSpeciesID != speciesMedicham {
		t.Errorf("ResolvedSpeciesID = %q, want %q (fallback to base)",
			result.ResolvedSpeciesID, speciesMedicham)
	}

	if !result.ShadowVariantMissing {
		t.Errorf("ShadowVariantMissing = false; fixture does not publish _shadow — must signal missing")
	}
}

// TestLevelFromCP_LuckyPurifiedAreNoOp pins the intentional no-op
// contract for Options.Lucky / Options.Purified in the info-path
// tools. CP inversion is stat-driven; Lucky is a stardust-only
// discount (powerup-side) and Purified affects costs, not stats.
// Both flags must be accepted without error and must yield the
// identical result as a no-flags call.
func TestLevelFromCP_LuckyPurifiedAreNoOp(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, levelFromCPFixture)
	handler := tools.NewLevelFromCPTool(mgr).Handler()

	base := tools.LevelFromCPParams{
		Species: speciesMedicham,
		IV:      [3]int{0, 15, 15},
		CP:      1500,
		XL:      true,
	}

	_, baseline, err := handler(t.Context(), nil, base)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	withFlags := base
	withFlags.Options = tools.CombatantOptions{Lucky: true, Purified: true}

	_, flagged, err := handler(t.Context(), nil, withFlags)
	if err != nil {
		t.Fatalf("flagged: %v", err)
	}

	if baseline.Level != flagged.Level ||
		baseline.CP != flagged.CP ||
		baseline.StatProduct != flagged.StatProduct {
		t.Errorf("Lucky/Purified must be no-op on CP inversion; got baseline=%+v flagged=%+v",
			baseline, flagged)
	}
}
