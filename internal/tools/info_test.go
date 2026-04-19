package tools_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

// movePsychic is the legacy-on-medicham move id exercised across
// the info lookup tests; hoisted to placate goconst.
const movePsychic = "PSYCHIC"

// infoFixtureGamemaster carries two species — medicham with legacy
// moves declared, bulbasaur without — and a handful of moves sufficient
// for both species_info and move_info lookups. Kept small so tests
// stay readable; the parser-level legacy fixture lives in the engine.
const infoFixtureGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {
      "dex": 308,
      "speciesId": "medicham",
      "speciesName": "Medicham",
      "baseStats": {"atk": 121, "def": 152, "hp": 155},
      "types": ["fighting", "psychic"],
      "fastMoves": ["COUNTER"],
      "chargedMoves": ["ICE_PUNCH", "PSYCHIC"],
      "legacyMoves": ["PSYCHIC"],
      "family": {"id": "FAMILY_MEDICHAM", "parent": "meditite"},
      "released": true
    },
    {
      "dex": 1,
      "speciesId": "bulbasaur",
      "speciesName": "Bulbasaur",
      "baseStats": {"atk": 118, "def": 111, "hp": 128},
      "types": ["grass", "poison"],
      "fastMoves": ["VINE_WHIP"],
      "chargedMoves": ["SLUDGE_BOMB"],
      "family": {"id": "FAMILY_BULBASAUR", "evolutions": ["ivysaur"]},
      "released": true
    }
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "cooldown": 500},
    {"moveId": "PSYCHIC", "name": "Psychic", "type": "psychic",
     "power": 90, "energy": 55, "cooldown": 500},
    {"moveId": "VINE_WHIP", "name": "Vine Whip", "type": "grass",
     "power": 5, "energy": 0, "energyGain": 8, "cooldown": 1000, "turns": 2},
    {"moveId": "SLUDGE_BOMB", "name": "Sludge Bomb", "type": "poison",
     "power": 80, "energy": 50, "cooldown": 500}
  ]
}`

func TestSpeciesInfo_HappyPath(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, infoFixtureGamemaster)
	handler := tools.NewSpeciesInfoTool(mgr, nil).Handler()

	_, result, err := handler(t.Context(), nil, tools.SpeciesInfoParams{
		Species: speciesMedicham,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Species != speciesMedicham {
		t.Errorf("Species = %q, want %s", result.Species, speciesMedicham)
	}
	if result.Dex != 308 || result.Name != "Medicham" {
		t.Errorf("unexpected dex/name: %d/%q", result.Dex, result.Name)
	}
	if result.BaseStats.Atk != 121 {
		t.Errorf("BaseStats.Atk = %d, want 121", result.BaseStats.Atk)
	}
	if result.PreEvolution != "meditite" {
		t.Errorf("PreEvolution = %q, want meditite", result.PreEvolution)
	}
	if !slices.Equal(result.LegacyMoves, []string{"PSYCHIC"}) {
		t.Errorf("LegacyMoves = %v, want [PSYCHIC]", result.LegacyMoves)
	}

	var psyLegacy bool

	for _, move := range result.ChargedMoves {
		if move.ID == movePsychic {
			psyLegacy = move.Legacy

			break
		}
	}

	if !psyLegacy {
		t.Error("ChargedMoves[PSYCHIC].Legacy = false, want true")
	}
}

func TestSpeciesInfo_UnknownSpecies(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, infoFixtureGamemaster)
	handler := tools.NewSpeciesInfoTool(mgr, nil).Handler()

	_, _, err := handler(t.Context(), nil, tools.SpeciesInfoParams{
		Species: "missingno",
	})
	if !errors.Is(err, tools.ErrUnknownSpecies) {
		t.Errorf("error = %v, want wrapping ErrUnknownSpecies", err)
	}
}

func TestMoveInfo_HappyPath(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, infoFixtureGamemaster)
	handler := tools.NewMoveInfoTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.MoveInfoParams{
		MoveID: movePsychic,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.ID != movePsychic {
		t.Errorf("ID = %q, want %s", result.ID, movePsychic)
	}
	if result.Category != "charged" {
		t.Errorf("Category = %q, want charged", result.Category)
	}
	if result.Power != 90 {
		t.Errorf("Power = %d, want 90", result.Power)
	}
	if !slices.Equal(result.LegacyOnSpecies, []string{"medicham"}) {
		t.Errorf("LegacyOnSpecies = %v, want [medicham] (only species with PSYCHIC legacy)",
			result.LegacyOnSpecies)
	}
}

func TestMoveInfo_UnknownMove(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, infoFixtureGamemaster)
	handler := tools.NewMoveInfoTool(mgr).Handler()

	_, _, err := handler(t.Context(), nil, tools.MoveInfoParams{MoveID: "NOT_A_MOVE"})
	if !errors.Is(err, tools.ErrUnknownMove) {
		t.Errorf("error = %v, want wrapping ErrUnknownMove", err)
	}
}

func TestTypeMatchup_DoubleResistance(t *testing.T) {
	t.Parallel()

	handler := tools.NewTypeMatchupTool().Handler()

	// Grass vs Water(1.6) × Ground(1.6) = 2.56 — classic Swampert-
	// over-Water pattern used as a sanity check for the chart math.
	_, result, err := handler(t.Context(), nil, tools.TypeMatchupParams{
		AttackerType:  "grass",
		DefenderTypes: []string{"water", "ground"},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Multiplier < 2.55 || result.Multiplier > 2.57 {
		t.Errorf("Multiplier = %.4f, want ~2.56", result.Multiplier)
	}
}

func TestTypeMatchup_MissingAttackerType(t *testing.T) {
	t.Parallel()

	handler := tools.NewTypeMatchupTool().Handler()

	_, _, err := handler(t.Context(), nil, tools.TypeMatchupParams{
		DefenderTypes: []string{"water"},
	})
	if !errors.Is(err, tools.ErrMissingAttackerType) {
		t.Errorf("error = %v, want wrapping ErrMissingAttackerType", err)
	}
}

// TestTypeMatchup_UnknownAttackerType pins the round-2 fix: an
// attacker type outside pvpoke's 18 canonical types must surface
// ErrUnknownType instead of silently folding to neutral (1.0).
func TestTypeMatchup_UnknownAttackerType(t *testing.T) {
	t.Parallel()

	handler := tools.NewTypeMatchupTool().Handler()

	_, _, err := handler(t.Context(), nil, tools.TypeMatchupParams{
		AttackerType:  "cosmic",
		DefenderTypes: []string{"water"},
	})
	if !errors.Is(err, tools.ErrUnknownType) {
		t.Errorf("error = %v, want wrapping ErrUnknownType", err)
	}
}

// TestTypeMatchup_UnknownDefenderType mirrors the attacker validator
// for the defender list. Any entry outside the 18 canonical types
// surfaces ErrUnknownType — no silent neutral.
func TestTypeMatchup_UnknownDefenderType(t *testing.T) {
	t.Parallel()

	handler := tools.NewTypeMatchupTool().Handler()

	_, _, err := handler(t.Context(), nil, tools.TypeMatchupParams{
		AttackerType:  "grass",
		DefenderTypes: []string{"water", "krypton"},
	})
	if !errors.Is(err, tools.ErrUnknownType) {
		t.Errorf("error = %v, want wrapping ErrUnknownType", err)
	}
}

// TestTypeMatchup_CasingNormalized pins that both AttackerType and
// DefenderTypes echoed in the result are lowercased — previously
// only AttackerType was, giving inconsistent JSON shape.
func TestTypeMatchup_CasingNormalized(t *testing.T) {
	t.Parallel()

	handler := tools.NewTypeMatchupTool().Handler()

	_, result, err := handler(t.Context(), nil, tools.TypeMatchupParams{
		AttackerType:  "Grass",
		DefenderTypes: []string{"Water", "Ground"},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.AttackerType != "grass" {
		t.Errorf("AttackerType = %q, want grass", result.AttackerType)
	}

	wantDefenders := []string{"water", "ground"}
	if !slices.Equal(result.DefenderTypes, wantDefenders) {
		t.Errorf("DefenderTypes = %v, want %v", result.DefenderTypes, wantDefenders)
	}
}

// TestSpeciesInfo_NoGamemasterLoaded pins the defensive branch: a
// handler invoked before gamemaster.Manager has any data must
// surface ErrGamemasterNotLoaded rather than dereferencing a nil
// snapshot.
func TestSpeciesInfo_NoGamemasterLoaded(t *testing.T) {
	t.Parallel()

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    "http://example.invalid",
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	handler := tools.NewSpeciesInfoTool(mgr, nil).Handler()

	_, _, err = handler(t.Context(), nil, tools.SpeciesInfoParams{Species: speciesMedicham})
	if !errors.Is(err, tools.ErrGamemasterNotLoaded) {
		t.Errorf("error = %v, want wrapping ErrGamemasterNotLoaded", err)
	}
}

// TestMoveInfo_NoGamemasterLoaded mirrors the species_info test for
// pvp_move_info.
func TestMoveInfo_NoGamemasterLoaded(t *testing.T) {
	t.Parallel()

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    "http://example.invalid",
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	handler := tools.NewMoveInfoTool(mgr).Handler()

	_, _, err = handler(t.Context(), nil, tools.MoveInfoParams{MoveID: "COUNTER"})
	if !errors.Is(err, tools.ErrGamemasterNotLoaded) {
		t.Errorf("error = %v, want wrapping ErrGamemasterNotLoaded", err)
	}
}

// TestMoveInfo_NonLegacyMoveEmptySlice pins the wire-shape
// invariant: a move that is not legacy on any species must emit
// `"legacy_on_species": []` (empty non-nil slice), never `null`.
// Matches ResolvedCombatant.ChargedMoves / SpeciesInfoMoveRef
// conventions.
func TestMoveInfo_NonLegacyMoveEmptySlice(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, infoFixtureGamemaster)
	handler := tools.NewMoveInfoTool(mgr).Handler()

	// COUNTER is not legacy on any fixture species (PSYCHIC is the
	// only legacy-flagged move on medicham).
	_, result, err := handler(t.Context(), nil, tools.MoveInfoParams{MoveID: "COUNTER"})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.LegacyOnSpecies == nil {
		t.Errorf("LegacyOnSpecies = nil, want empty non-nil slice")
	}
	if len(result.LegacyOnSpecies) != 0 {
		t.Errorf("LegacyOnSpecies = %v, want empty", result.LegacyOnSpecies)
	}
}

// speciesInfoRankingsFixture carries a tiny rankings payload naming
// medicham so TestSpeciesInfo_LeagueRanksFromManager can exercise
// the best-effort per-league lookup end-to-end. Only the overall
// Great League is populated; the other three leagues 404 so the
// best-effort tolerance kicks in (and the test confirms exactly
// one LeagueRank entry comes back).
const speciesInfoRankingsFixture = `[
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 800, "score": 92,
   "moveset": ["COUNTER", "ICE_PUNCH", "PSYCHIC"], "matchups": [], "counters": [],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}}
]`

// TestSpeciesInfo_EmptyListsNotNull pins the wire-shape invariant
// for optional list fields. Bulbasaur in the fixture has no
// legacyMoves, no family.evolutions (only .evolutions=[ivysaur] on
// the ivy side — wait, bulb DOES have evolutions), and no tags.
// Build a minimal fixture species without any list fields and
// assert that LegacyMoves / Evolutions / Tags serialise as []
// rather than null.
func TestSpeciesInfo_EmptyListsNotNull(t *testing.T) {
	t.Parallel()

	// Minimal species with no legacyMoves, no family block, no tags.
	const bareFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {
      "dex": 132,
      "speciesId": "ditto",
      "speciesName": "Ditto",
      "baseStats": {"atk": 91, "def": 91, "hp": 134},
      "types": ["normal"],
      "fastMoves": ["POUND"],
      "chargedMoves": ["STRUGGLE"],
      "released": true
    }
  ],
  "moves": [
    {"moveId": "POUND", "name": "Pound", "type": "normal",
     "power": 4, "energy": 0, "energyGain": 4, "cooldown": 1000, "turns": 2},
    {"moveId": "STRUGGLE", "name": "Struggle", "type": "normal",
     "power": 35, "energy": 33, "cooldown": 500}
  ]
}`

	mgr := newManagerWithFixture(t, bareFixture)
	handler := tools.NewSpeciesInfoTool(mgr, nil).Handler()

	_, result, err := handler(t.Context(), nil, tools.SpeciesInfoParams{Species: "ditto"})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	raw := string(payload)

	for _, field := range []string{"legacy_moves", "evolutions", "tags"} {
		wantEmpty := `"` + field + `":[]`
		badNull := `"` + field + `":null`

		if strings.Contains(raw, badNull) {
			t.Errorf("JSON contains %q, want %q: %s", badNull, wantEmpty, raw)
		}
	}

	if result.LegacyMoves == nil {
		t.Error("LegacyMoves = nil, want non-nil empty slice")
	}
	if result.Evolutions == nil {
		t.Error("Evolutions = nil, want non-nil empty slice")
	}
	if result.Tags == nil {
		t.Error("Tags = nil, want non-nil empty slice")
	}
}

// TestTypeMatchup_TooManyDefenderTypes pins the ≤2 defender-types
// guard. Niantic caps Pokémon at 2 types; a 3-entry list is
// malformed input that previously produced a plausible-looking
// multiplier with no way for the caller to detect the nonsense.
func TestTypeMatchup_TooManyDefenderTypes(t *testing.T) {
	t.Parallel()

	handler := tools.NewTypeMatchupTool().Handler()

	_, _, err := handler(t.Context(), nil, tools.TypeMatchupParams{
		AttackerType:  "grass",
		DefenderTypes: []string{"water", "ground", "rock"},
	})
	if !errors.Is(err, tools.ErrTooManyDefenderTypes) {
		t.Errorf("error = %v, want wrapping ErrTooManyDefenderTypes", err)
	}
}

// TestSpeciesInfo_LeagueRanksFromManager exercises the branch in
// lookupLeagueRanks that the happy-path (ranks=nil) skipped
// entirely — iterate leagues, fetch rankings, find the species,
// record rank + rating, tolerate 404s on unsupported (cup, cap)
// pairs silently.
func TestSpeciesInfo_LeagueRanksFromManager(t *testing.T) {
	t.Parallel()

	rankServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only Great returns data; the other three leagues 404 so
		// the best-effort loop must handle the error path too.
		if r.URL.Path != "/all/overall/rankings-1500.json" {
			http.NotFound(w, r)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(speciesInfoRankingsFixture))
	}))
	t.Cleanup(rankServer.Close)

	ranks, err := rankings.NewManager(rankings.Config{
		BaseURL:  rankServer.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("rankings.NewManager: %v", err)
	}

	mgr := newManagerWithFixture(t, infoFixtureGamemaster)
	handler := tools.NewSpeciesInfoTool(mgr, ranks).Handler()

	_, result, err := handler(t.Context(), nil, tools.SpeciesInfoParams{Species: speciesMedicham})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.LeagueRanks) != 1 {
		t.Fatalf("LeagueRanks len = %d, want 1 (great only)", len(result.LeagueRanks))
	}

	entry := result.LeagueRanks[0]

	if entry.League != leagueGreat {
		t.Errorf("LeagueRanks[0].League = %q, want %s", entry.League, leagueGreat)
	}
	if entry.Rank != 1 {
		t.Errorf("LeagueRanks[0].Rank = %d, want 1", entry.Rank)
	}
	if entry.Rating != 800 {
		t.Errorf("LeagueRanks[0].Rating = %d, want 800", entry.Rating)
	}
}
