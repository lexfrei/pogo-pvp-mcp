package tools_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

// threatCoverageFixtureGamemaster: a trimmed gamemaster with three
// species and a shared fast+charged pair so team/pool/meta all
// resolve without cache skew.
const threatCoverageFixtureGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 1, "speciesId": "a", "speciesName": "A",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true},
    {"dex": 2, "speciesId": "b", "speciesName": "B",
     "baseStats": {"atk": 152, "def": 143, "hp": 216},
     "types": ["water", "ground"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true},
    {"dex": 3, "speciesId": "c", "speciesName": "C",
     "baseStats": {"atk": 234, "def": 159, "hp": 207},
     "types": ["fighting"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true}
  ],
  "moves": [
    {"moveId": "FAST1", "name": "Fast 1", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 5, "cooldown": 1000, "turns": 2},
    {"moveId": "CH1", "name": "Charged 1", "type": "normal",
     "power": 50, "energy": 35, "cooldown": 500}
  ]
}`

const threatCoverageRankingsFixture = `[
  {"speciesId": "a", "speciesName": "A", "rating": 700, "score": 95,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 107, "def": 139, "hp": 141}},
  {"speciesId": "b", "speciesName": "B", "rating": 680, "score": 93,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 111, "def": 113, "hp": 161}},
  {"speciesId": "c", "speciesName": "C", "rating": 650, "score": 90,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 1900, "atk": 125, "def": 120, "hp": 130}}
]`

func newThreatCoverageTool(t *testing.T) *tools.ThreatCoverageTool {
	t.Helper()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(threatCoverageFixtureGamemaster))
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
		_, _ = w.Write([]byte(threatCoverageRankingsFixture))
	}))
	t.Cleanup(rankServer.Close)

	ranksMgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  rankServer.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("NewManager rankings: %v", err)
	}

	return tools.NewThreatCoverageTool(gmMgr, ranksMgr)
}

// TestThreatCoverage_HappyPath verifies the full round-trip: team +
// pool + cup resolved, meta populated, TeamCoverage populated, and
// the Team slice echoes the resolved movesets.
func TestThreatCoverage_HappyPath(t *testing.T) {
	t.Parallel()

	tool := newThreatCoverageTool(t)
	handler := tool.Handler()

	member := tools.Combatant{
		IV: [3]int{15, 15, 15}, Level: 40,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}
	memberA := member
	memberA.Species = "a"
	memberB := member
	memberB.Species = "b"
	memberC := member
	memberC.Species = "c"

	_, result, err := handler(t.Context(), nil, tools.ThreatCoverageParams{
		Team:          []tools.Combatant{memberA, memberB, memberC},
		CandidatePool: []tools.Combatant{memberA, memberB, memberC},
		League:        leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.League != leagueGreat {
		t.Errorf("League = %q, want %q", result.League, leagueGreat)
	}
	if result.Cup != cupAllLabel {
		t.Errorf("Cup = %q, want %q (default)", result.Cup, cupAllLabel)
	}
	if result.MetaSize != 3 {
		t.Errorf("MetaSize = %d, want 3 (fixture size)", result.MetaSize)
	}
	if len(result.Team) != 3 {
		t.Errorf("Team len = %d, want 3", len(result.Team))
	}
	if len(result.TeamCoverage) == 0 {
		t.Errorf("TeamCoverage is empty; want at least one entry")
	}
}

// TestThreatCoverage_WrongTeamSize rejects teams of size != 3.
func TestThreatCoverage_WrongTeamSize(t *testing.T) {
	t.Parallel()

	tool := newThreatCoverageTool(t)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.ThreatCoverageParams{
		Team:   []tools.Combatant{{Species: "a", Level: 40, FastMove: "FAST1"}},
		League: leagueGreat,
	})
	if !errors.Is(err, tools.ErrTeamSizeMismatch) {
		t.Errorf("error = %v, want wrapping ErrTeamSizeMismatch", err)
	}
}

// TestThreatCoverage_PoolTooLarge rejects pools exceeding MaxPoolSize.
func TestThreatCoverage_PoolTooLarge(t *testing.T) {
	t.Parallel()

	tool := newThreatCoverageTool(t)
	handler := tool.Handler()

	valid := tools.Combatant{
		Species: "a", IV: [3]int{15, 15, 15}, Level: 40,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}
	team := []tools.Combatant{valid, valid, valid}

	pool := make([]tools.Combatant, tools.MaxPoolSize+1)
	for i := range pool {
		pool[i] = valid
	}

	_, _, err := handler(t.Context(), nil, tools.ThreatCoverageParams{
		Team: team, CandidatePool: pool, League: leagueGreat,
	})
	if !errors.Is(err, tools.ErrPoolTooLarge) {
		t.Errorf("error = %v, want wrapping ErrPoolTooLarge", err)
	}
}

// TestThreatCoverage_UnknownLeague rejects invalid league names.
func TestThreatCoverage_UnknownLeague(t *testing.T) {
	t.Parallel()

	tool := newThreatCoverageTool(t)
	handler := tool.Handler()

	valid := tools.Combatant{
		Species: "a", IV: [3]int{15, 15, 15}, Level: 40,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}
	team := []tools.Combatant{valid, valid, valid}

	_, _, err := handler(t.Context(), nil, tools.ThreatCoverageParams{
		Team: team, League: "marshmallow",
	})
	if !errors.Is(err, tools.ErrUnknownLeague) {
		t.Errorf("error = %v, want wrapping ErrUnknownLeague", err)
	}
}

// TestThreatCoverage_GamemasterNotLoaded surfaces the cold-start
// sentinel.
func TestThreatCoverage_GamemasterNotLoaded(t *testing.T) {
	t.Parallel()

	gmMgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    "http://127.0.0.1:1",
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	ranksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(ranksServer.Close)

	ranksMgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  ranksServer.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("NewManager rankings: %v", err)
	}

	tool := tools.NewThreatCoverageTool(gmMgr, ranksMgr)
	handler := tool.Handler()

	valid := tools.Combatant{
		Species: "a", IV: [3]int{15, 15, 15}, Level: 40,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}

	_, _, err = handler(t.Context(), nil, tools.ThreatCoverageParams{
		Team:   []tools.Combatant{valid, valid, valid},
		League: leagueGreat,
	})
	if !errors.Is(err, tools.ErrGamemasterNotLoaded) {
		t.Errorf("error = %v, want wrapping ErrGamemasterNotLoaded", err)
	}
}

// TestThreatCoverage_UncoveredThreatsSurfaceCandidates validates the
// core contract: when the team coverage against a meta species is
// below the uncoveredThreshold, an entry appears in
// UncoveredThreats; the pool members that cover it (rating ≥
// threshold) are listed with battle_rating and sorted desc.
func TestThreatCoverage_UncoveredThreatsSurfaceCandidates(t *testing.T) {
	t.Parallel()

	tool := newThreatCoverageTool(t)
	handler := tool.Handler()

	weak := tools.Combatant{
		Species: "a", IV: [3]int{0, 0, 0}, Level: 1,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}
	strong := tools.Combatant{
		Species: "c", IV: [3]int{15, 15, 15}, Level: 40,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}

	_, result, err := handler(t.Context(), nil, tools.ThreatCoverageParams{
		Team:          []tools.Combatant{weak, weak, weak},
		CandidatePool: []tools.Combatant{strong, strong, strong},
		League:        leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.UncoveredThreats) == 0 {
		t.Fatalf("UncoveredThreats is empty; a level-1 IV-0 team must fail to cover at least one meta entry")
	}

	var entriesWithCandidates int

	for _, entry := range result.UncoveredThreats {
		if entry.TeamBestRating >= 400 {
			t.Errorf("UncoveredThreats[%q].TeamBestRating = %d, want < 400 (uncoveredThreshold)",
				entry.Threat, entry.TeamBestRating)
		}

		if len(entry.CandidatesThatCover) > 0 {
			entriesWithCandidates++
		}

		// Each candidate must score ≥ 400.
		for _, c := range entry.CandidatesThatCover {
			if c.BattleRating < 400 {
				t.Errorf("threat %q candidate %q rating %d < 400 (uncoveredThreshold)",
					entry.Threat, c.Counter.Species, c.BattleRating)
			}
		}

		// Sort invariant: descending by rating.
		for i := 1; i < len(entry.CandidatesThatCover); i++ {
			if entry.CandidatesThatCover[i].BattleRating >
				entry.CandidatesThatCover[i-1].BattleRating {
				t.Errorf("CandidatesThatCover for threat %q not sorted desc", entry.Threat)
			}
		}
	}

	if entriesWithCandidates == 0 {
		t.Fatalf("no uncovered threat produced a covering candidate; the level-40 pool must surface as a counter for at least one threat")
	}
}

// TestThreatCoverage_SkippedMetaSurfaced pins the cache-skew path:
// a ranking entry whose species is not in the gamemaster snapshot
// must be dropped from the sweep and surfaced in
// SkippedMetaSpecies, so the caller can tell "not covered" from
// "never simulated" (which looks like rating=0 in TeamCoverage).
func TestThreatCoverage_SkippedMetaSurfaced(t *testing.T) {
	t.Parallel()

	const skewedRanks = `[
  {"speciesId": "a", "speciesName": "A", "rating": 700,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2100, "atk": 107, "def": 139, "hp": 141}},
  {"speciesId": "phantom", "speciesName": "Phantom", "rating": 680,
   "moveset": ["FAST1", "CH1"],
   "stats": {"product": 2000, "atk": 111, "def": 113, "hp": 161}}
]`

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(threatCoverageFixtureGamemaster))
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
		_, _ = w.Write([]byte(skewedRanks))
	}))
	t.Cleanup(rankServer.Close)

	ranksMgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  rankServer.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("NewManager rankings: %v", err)
	}

	tool := tools.NewThreatCoverageTool(gmMgr, ranksMgr)
	handler := tool.Handler()

	valid := tools.Combatant{
		Species: "a", IV: [3]int{15, 15, 15}, Level: 40,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}

	_, result, err := handler(t.Context(), nil, tools.ThreatCoverageParams{
		Team:   []tools.Combatant{valid, valid, valid},
		League: leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if !slices.Contains(result.SkippedMetaSpecies, "phantom") {
		t.Errorf("SkippedMetaSpecies = %v, want to contain \"phantom\"", result.SkippedMetaSpecies)
	}

	// phantom must not leak into TeamCoverage or UncoveredThreats —
	// it was dropped before any simulation ran.
	for _, entry := range result.UncoveredThreats {
		if entry.Threat == "phantom" {
			t.Errorf("UncoveredThreats should not contain skipped species \"phantom\"")
		}
	}

	if _, present := result.TeamCoverage["phantom"]; present {
		t.Errorf("TeamCoverage should not contain skipped species \"phantom\"")
	}
}

// TestThreatCoverage_ContextCanceled pins the post-sweep cancellation
// check: a context canceled before the handler completes produces an
// error wrapping context.Canceled, not a truncated success.
func TestThreatCoverage_ContextCanceled(t *testing.T) {
	t.Parallel()

	tool := newThreatCoverageTool(t)
	handler := tool.Handler()

	valid := tools.Combatant{
		Species: "a", IV: [3]int{15, 15, 15}, Level: 40,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err := handler(ctx, nil, tools.ThreatCoverageParams{
		Team:   []tools.Combatant{valid, valid, valid},
		League: leagueGreat,
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want wrapping context.Canceled", err)
	}
}

// TestThreatCoverage_InvalidShieldsValue rejects out-of-range shield
// values.
func TestThreatCoverage_InvalidShieldsValue(t *testing.T) {
	t.Parallel()

	tool := newThreatCoverageTool(t)
	handler := tool.Handler()

	valid := tools.Combatant{
		Species: "a", IV: [3]int{15, 15, 15}, Level: 40,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}
	team := []tools.Combatant{valid, valid, valid}

	_, _, err := handler(t.Context(), nil, tools.ThreatCoverageParams{
		Team: team, League: leagueGreat,
		Shields: []int{3},
	})
	if !errors.Is(err, tools.ErrInvalidShields) {
		t.Errorf("error = %v, want wrapping ErrInvalidShields", err)
	}
}
