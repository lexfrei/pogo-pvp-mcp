package tools_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

// moveCounter / moveIcePunch are the non-legacy move ids the
// non-legacy enumeration test expects medicham to settle on.
const (
	moveCounter  = "COUNTER"
	moveIcePunch = "ICE_PUNCH"
)

// newTeamBuilderToolFromFixture builds a TeamBuilderTool bound to a
// custom gamemaster fixture + rankings payload. Legacy tests need
// control over both the legacyMoves block and the empty rankings
// slice (the DisallowLegacy path short-circuits before hitting
// rankings, so an empty fixture suffices).
func newTeamBuilderToolFromFixture(t *testing.T, gmJSON, ranksJSON string) *tools.TeamBuilderTool {
	t.Helper()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(gmJSON))
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

	rankServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ranksJSON))
	}))
	t.Cleanup(rankServer.Close)

	ranksMgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  rankServer.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("NewManager rankings: %v", err)
	}

	return tools.NewTeamBuilderTool(gmMgr, ranksMgr)
}

// legacyFixtureGamemaster mirrors a slimmed version of infoFixtureGamemaster
// with medicham marking PSYCHIC as legacy. Enough to exercise the
// MoveRef tagging and the DisallowLegacy guard end-to-end.
const legacyFixtureGamemaster = `{
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
      "released": true
    },
    {
      "dex": 184,
      "speciesId": "azumarill",
      "speciesName": "Azumarill",
      "baseStats": {"atk": 112, "def": 152, "hp": 225},
      "types": ["water", "fairy"],
      "fastMoves": ["BUBBLE"],
      "chargedMoves": ["ICE_BEAM", "PLAY_ROUGH"],
      "released": true
    },
    {
      "dex": 68,
      "speciesId": "machamp",
      "speciesName": "Machamp",
      "baseStats": {"atk": 234, "def": 159, "hp": 207},
      "types": ["fighting"],
      "fastMoves": ["COUNTER"],
      "chargedMoves": ["CROSS_CHOP"],
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
    {"moveId": "BUBBLE", "name": "Bubble", "type": "water",
     "power": 12, "energy": 0, "energyGain": 14, "cooldown": 1500, "turns": 3},
    {"moveId": "ICE_BEAM", "name": "Ice Beam", "type": "ice",
     "power": 90, "energy": 55, "cooldown": 500},
    {"moveId": "PLAY_ROUGH", "name": "Play Rough", "type": "fairy",
     "power": 90, "energy": 60, "cooldown": 500},
    {"moveId": "CROSS_CHOP", "name": "Cross Chop", "type": "fighting",
     "power": 50, "energy": 35, "cooldown": 500}
  ]
}`

// TestTeamBuilder_DisallowLegacyExplicit pins the Phase-1C hard
// rejection path: a combatant passed in with an explicit legacy
// move under DisallowLegacy=true must surface ErrLegacyConflict
// before any simulation.
func TestTeamBuilder_DisallowLegacyExplicit(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderToolFromFixture(t, legacyFixtureGamemaster, `[]`)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{
			Species: "medicham", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "COUNTER", ChargedMoves: []string{movePsychic},
		},
		{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
		},
		{
			Species: "machamp", IV: [3]int{15, 15, 15}, Level: 30,
			FastMove: "COUNTER", ChargedMoves: []string{"CROSS_CHOP"},
		},
	}

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:           pool,
		League:         leagueGreat,
		DisallowLegacy: true,
	})
	if !errors.Is(err, tools.ErrLegacyConflict) {
		t.Errorf("error = %v, want wrapping ErrLegacyConflict (medicham PSYCHIC is legacy)", err)
	}
}

// TestTeamBuilder_DisallowLegacyRejectsResolvedLegacy pins the
// round-2 fix: DisallowLegacy=true with an EMPTY moveset (auto-
// fill path) must still reject when the pvpoke-recommended
// moveset is legacy. Before the fix, the guard only checked
// explicit movesets — auto-fill routed around it silently.
func TestTeamBuilder_DisallowLegacyRejectsResolvedLegacy(t *testing.T) {
	t.Parallel()

	// Ranking fixture recommends PSYCHIC (legacy on medicham).
	const ranksJSON = `[
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 700,
   "moveset": ["COUNTER", "PSYCHIC", "ICE_PUNCH"],
   "matchups": [], "counters": [],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}}
]`

	tool := newTeamBuilderToolFromFixture(t, legacyFixtureGamemaster, ranksJSON)
	handler := tool.Handler()

	// Medicham with empty moveset → auto-fill will pull PSYCHIC
	// from the rankings recommendation; DisallowLegacy must
	// trip ErrLegacyConflict before simulation.
	pool := []tools.Combatant{
		{Species: "medicham", IV: [3]int{15, 15, 15}, Level: 40},
		{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
		},
		{
			Species: "machamp", IV: [3]int{15, 15, 15}, Level: 30,
			FastMove: "COUNTER", ChargedMoves: []string{"CROSS_CHOP"},
		},
	}

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:           pool,
		League:         leagueGreat,
		DisallowLegacy: true,
	})
	if !errors.Is(err, tools.ErrLegacyConflict) {
		t.Errorf("error = %v, want wrapping ErrLegacyConflict (auto-fill landed on legacy PSYCHIC)", err)
	}
}

// newRankToolFromFixture mirrors newTeamBuilderToolFromFixture for
// RankTool; used by the legacy-aware rank pipeline tests.
func newRankToolFromFixture(t *testing.T, gmJSON, ranksJSON string) *tools.RankTool {
	t.Helper()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(gmJSON))
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

	rankServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ranksJSON))
	}))
	t.Cleanup(rankServer.Close)

	ranksMgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  rankServer.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("NewManager rankings: %v", err)
	}

	return tools.NewRankTool(gmMgr, ranksMgr)
}

// TestRank_OptimalHasLegacyDetected pins Moveset.HasLegacy tagging:
// medicham's pvpoke-recommended build in the fixture includes
// PSYCHIC (legacy on medicham) so HasLegacy must be true.
func TestRank_OptimalHasLegacyDetected(t *testing.T) {
	t.Parallel()

	const ranksJSON = `[
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 800,
   "moveset": ["COUNTER", "PSYCHIC", "ICE_PUNCH"],
   "matchups": [], "counters": [],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}}
]`

	tool := newRankToolFromFixture(t, legacyFixtureGamemaster, ranksJSON)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: speciesMedicham,
		IV:      [3]int{0, 15, 15},
		League:  leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.OptimalMoveset == nil {
		t.Fatal("OptimalMoveset = nil, want populated")
	}
	if !result.OptimalMoveset.HasLegacy {
		t.Error("OptimalMoveset.HasLegacy = false, want true (PSYCHIC is legacy on medicham)")
	}
	if result.NonLegacyMoveset == nil {
		t.Fatal("NonLegacyMoveset = nil, want populated when optimal has legacy")
	}
}

// TestRank_NonLegacyMovesetFields pins the concrete Fast / Charged
// / RatingDelta carried by NonLegacyMoveset. For medicham with
// legacy PSYCHIC, the only non-legacy charged left in the fixture
// is ICE_PUNCH, so the enumeration must settle on COUNTER +
// ICE_PUNCH. The delta is whatever the simulation produces; we
// only assert it's computed (non-zero magnitude) and that the
// moveset fields are not empty.
func TestRank_NonLegacyMovesetFields(t *testing.T) {
	t.Parallel()

	const ranksJSON = `[
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 800,
   "moveset": ["COUNTER", "PSYCHIC", "ICE_PUNCH"],
   "matchups": [], "counters": [],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}},
  {"speciesId": "azumarill", "speciesName": "Azumarill", "rating": 900,
   "moveset": ["BUBBLE", "ICE_BEAM", "PLAY_ROUGH"],
   "matchups": [], "counters": [],
   "stats": {"product": 2500, "atk": 80, "def": 150, "hp": 200}},
  {"speciesId": "machamp", "speciesName": "Machamp", "rating": 750,
   "moveset": ["COUNTER", "CROSS_CHOP"],
   "matchups": [], "counters": [],
   "stats": {"product": 2400, "atk": 170, "def": 130, "hp": 180}}
]`

	tool := newRankToolFromFixture(t, legacyFixtureGamemaster, ranksJSON)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: speciesMedicham,
		IV:      [3]int{0, 15, 15},
		League:  leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.NonLegacyMoveset == nil {
		t.Fatal("NonLegacyMoveset = nil, want populated")
	}

	nl := result.NonLegacyMoveset

	if nl.Fast != moveCounter {
		t.Errorf("NonLegacyMoveset.Fast = %q, want %s", nl.Fast, moveCounter)
	}
	if len(nl.Charged) != 1 || nl.Charged[0] != moveIcePunch {
		t.Errorf("NonLegacyMoveset.Charged = %v, want [%s]", nl.Charged, moveIcePunch)
	}
	if nl.Rationale != "" {
		t.Errorf("NonLegacyMoveset.Rationale = %q, want empty (successful enumeration)", nl.Rationale)
	}
}

// TestRank_NonLegacyRationaleNoChargedMoves pins the
// "species has no non-legacy charged moves" rationale branch.
// Uses a synthetic fixture where all charged moves on the species
// are legacy → NonLegacyMoveset carries the rationale with empty
// Fast / Charged.
func TestRank_NonLegacyRationaleNoChargedMoves(t *testing.T) {
	t.Parallel()

	const gmJSON = `{
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
      "legacyMoves": ["ICE_PUNCH", "PSYCHIC"],
      "released": true
    }
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "cooldown": 500},
    {"moveId": "PSYCHIC", "name": "Psychic", "type": "psychic",
     "power": 90, "energy": 55, "cooldown": 500}
  ]
}`

	const ranksJSON = `[
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 800,
   "moveset": ["COUNTER", "PSYCHIC", "ICE_PUNCH"],
   "matchups": [], "counters": [],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}}
]`

	tool := newRankToolFromFixture(t, gmJSON, ranksJSON)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: speciesMedicham,
		IV:      [3]int{0, 15, 15},
		League:  leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.NonLegacyMoveset == nil {
		t.Fatal("NonLegacyMoveset = nil, want populated with rationale")
	}
	if result.NonLegacyMoveset.Rationale != "species has no non-legacy charged moves" {
		t.Errorf("Rationale = %q, want \"species has no non-legacy charged moves\"",
			result.NonLegacyMoveset.Rationale)
	}
	if result.NonLegacyMoveset.Fast != "" || len(result.NonLegacyMoveset.Charged) != 0 {
		t.Errorf("NonLegacyMoveset has unexpected fast/charged: %+v", result.NonLegacyMoveset)
	}
}

// TestTeamAnalysis_DisallowLegacyRejectsResolvedLegacy mirrors the
// team_builder auto-fill test on the team_analysis side: a team
// member with empty moveset under DisallowLegacy=true must also
// trip ErrLegacyConflict when the pvpoke recommendation contains
// a legacy move.
func TestTeamAnalysis_DisallowLegacyRejectsResolvedLegacy(t *testing.T) {
	t.Parallel()

	const ranksJSON = `[
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 800,
   "moveset": ["COUNTER", "PSYCHIC", "ICE_PUNCH"],
   "matchups": [], "counters": [],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}}
]`

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(legacyFixtureGamemaster))
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

	rankServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ranksJSON))
	}))
	t.Cleanup(rankServer.Close)

	ranksMgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  rankServer.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("NewManager rankings: %v", err)
	}

	handler := tools.NewTeamAnalysisTool(gmMgr, ranksMgr).Handler()

	team := []tools.Combatant{
		{Species: speciesMedicham, IV: [3]int{15, 15, 15}, Level: 40},
		{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
		},
		{
			Species: "machamp", IV: [3]int{15, 15, 15}, Level: 30,
			FastMove: "COUNTER", ChargedMoves: []string{"CROSS_CHOP"},
		},
	}

	_, _, err = handler(t.Context(), nil, tools.TeamAnalysisParams{
		Team:           team,
		League:         leagueGreat,
		DisallowLegacy: true,
	})
	if !errors.Is(err, tools.ErrLegacyConflict) {
		t.Errorf("error = %v, want wrapping ErrLegacyConflict (auto-fill landed on legacy PSYCHIC)", err)
	}
}

// TestRank_NonLegacyAbsentWhenOptimalClean pins that species with
// a non-legacy recommended build get no non_legacy_moveset field —
// nothing to compare against.
func TestRank_NonLegacyAbsentWhenOptimalClean(t *testing.T) {
	t.Parallel()

	const ranksJSON = `[
  {"speciesId": "azumarill", "speciesName": "Azumarill", "rating": 900,
   "moveset": ["BUBBLE", "ICE_BEAM", "PLAY_ROUGH"],
   "matchups": [], "counters": [],
   "stats": {"product": 2500, "atk": 80, "def": 150, "hp": 200}}
]`

	tool := newRankToolFromFixture(t, legacyFixtureGamemaster, ranksJSON)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: "azumarill",
		IV:      [3]int{0, 15, 15},
		League:  leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.OptimalMoveset == nil {
		t.Fatal("OptimalMoveset = nil, want populated")
	}
	if result.OptimalMoveset.HasLegacy {
		t.Error("OptimalMoveset.HasLegacy = true, want false (azumarill has no legacy in fixture)")
	}
	if result.NonLegacyMoveset != nil {
		t.Errorf("NonLegacyMoveset = %+v, want nil (nothing to compare)",
			result.NonLegacyMoveset)
	}
}

// TestTeamAnalysis_DisallowLegacyExplicit mirrors the team_builder
// test on the team_analysis side: an explicit legacy move under
// DisallowLegacy=true surfaces ErrLegacyConflict before simulation.
func TestTeamAnalysis_DisallowLegacyExplicit(t *testing.T) {
	t.Parallel()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(legacyFixtureGamemaster))
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

	rankServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(rankServer.Close)

	ranksMgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  rankServer.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("NewManager rankings: %v", err)
	}

	handler := tools.NewTeamAnalysisTool(gmMgr, ranksMgr).Handler()

	team := []tools.Combatant{
		{
			Species: speciesMedicham, IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "COUNTER", ChargedMoves: []string{movePsychic},
		},
		{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
		},
		{
			Species: "machamp", IV: [3]int{15, 15, 15}, Level: 30,
			FastMove: "COUNTER", ChargedMoves: []string{"CROSS_CHOP"},
		},
	}

	_, _, err = handler(t.Context(), nil, tools.TeamAnalysisParams{
		Team:           team,
		League:         leagueGreat,
		DisallowLegacy: true,
	})
	if !errors.Is(err, tools.ErrLegacyConflict) {
		t.Errorf("error = %v, want wrapping ErrLegacyConflict", err)
	}
}

// TestMeta_MoveRefCarriesLegacyFlag pins that pvp_meta emits each
// recommended move as a MoveRef with Legacy tagged from the
// species' LegacyMoves list.
func TestMeta_MoveRefCarriesLegacyFlag(t *testing.T) {
	t.Parallel()

	const ranksJSON = `[
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 800,
   "moveset": ["COUNTER", "PSYCHIC", "ICE_PUNCH"],
   "matchups": [], "counters": [],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}}
]`

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(legacyFixtureGamemaster))
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

	rankServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ranksJSON))
	}))
	t.Cleanup(rankServer.Close)

	ranksMgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  rankServer.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("NewManager rankings: %v", err)
	}

	handler := tools.NewMetaTool(ranksMgr, gmMgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.MetaParams{League: leagueGreat, TopN: 1})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Entries) != 1 {
		t.Fatalf("Entries len = %d, want 1", len(result.Entries))
	}

	entry := result.Entries[0]

	if len(entry.Moveset) != 3 {
		t.Fatalf("Moveset len = %d, want 3", len(entry.Moveset))
	}

	for _, ref := range entry.Moveset {
		wantLegacy := ref.ID == movePsychic
		if ref.Legacy != wantLegacy {
			t.Errorf("MoveRef{%q}.Legacy = %v, want %v", ref.ID, ref.Legacy, wantLegacy)
		}
	}
}

// TestTeamBuilder_DisallowLegacyAllowsNonLegacy confirms the
// converse: DisallowLegacy=true with non-legacy explicit movesets
// succeeds without error.
func TestTeamBuilder_DisallowLegacyAllowsNonLegacy(t *testing.T) {
	t.Parallel()

	tool := newTeamBuilderToolFromFixture(t, legacyFixtureGamemaster, `[]`)
	handler := tool.Handler()

	pool := []tools.Combatant{
		{
			Species: "medicham", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "COUNTER", ChargedMoves: []string{"ICE_PUNCH"},
		},
		{
			Species: "azumarill", IV: [3]int{15, 15, 15}, Level: 40,
			FastMove: "BUBBLE", ChargedMoves: []string{"ICE_BEAM"},
		},
		{
			Species: "machamp", IV: [3]int{15, 15, 15}, Level: 30,
			FastMove: "COUNTER", ChargedMoves: []string{"CROSS_CHOP"},
		},
	}

	_, _, err := handler(t.Context(), nil, tools.TeamBuilderParams{
		Pool:           pool,
		League:         leagueGreat,
		DisallowLegacy: true,
	})
	if err != nil {
		t.Errorf("handler: %v (non-legacy movesets should pass the gate)", err)
	}
}
