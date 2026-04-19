package tools_test

import (
	"encoding/json"
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

const leagueGreat = "great"

// teamAnalysisFixtureGamemaster is a trimmed gamemaster with three
// species + enough moves so meta combatant resolution and user team
// resolution both succeed.
const teamAnalysisFixtureGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-18 00:00:00",
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
     "types": ["fighting", "none"],
     "fastMoves": ["FAST1"], "chargedMoves": ["CH1"], "released": true}
  ],
  "moves": [
    {"moveId": "FAST1", "name": "Fast 1", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 5, "cooldown": 1000, "turns": 2},
    {"moveId": "CH1", "name": "Charged 1", "type": "normal",
     "power": 50, "energy": 35, "energyGain": 0, "cooldown": 500}
  ]
}`

const teamAnalysisRankingsFixture = `[
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

func newTeamAnalysisTool(t *testing.T) *tools.TeamAnalysisTool {
	t.Helper()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(teamAnalysisFixtureGamemaster))
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
		_, _ = w.Write([]byte(teamAnalysisRankingsFixture))
	}))
	t.Cleanup(rankServer.Close)

	ranksMgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  rankServer.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("NewManager rankings: %v", err)
	}

	return tools.NewTeamAnalysisTool(gmMgr, ranksMgr)
}

func TestTeamAnalysisTool_HappyPath(t *testing.T) {
	t.Parallel()

	tool := newTeamAnalysisTool(t)
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

	_, result, err := handler(t.Context(), nil, tools.TeamAnalysisParams{
		Team:   []tools.Combatant{memberA, memberB, memberC},
		League: leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.League != leagueGreat {
		t.Errorf("League = %q, want %q", result.League, leagueGreat)
	}
	if result.MetaSize != 3 {
		t.Errorf("MetaSize = %d, want 3 (fixture size)", result.MetaSize)
	}
	if len(result.PerMember) != 3 {
		t.Fatalf("PerMember len = %d, want 3", len(result.PerMember))
	}
	for i, member := range result.PerMember {
		if member.AvgRating < 0 || member.AvgRating > 1000 {
			t.Errorf("PerMember[%d] AvgRating %.2f outside [0, 1000]", i, member.AvgRating)
		}
	}
	if result.TeamScore < 0 || result.TeamScore > 1000 {
		t.Errorf("TeamScore %.2f outside [0, 1000]", result.TeamScore)
	}
}

func TestTeamAnalysisTool_WrongTeamSize(t *testing.T) {
	t.Parallel()

	tool := newTeamAnalysisTool(t)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.TeamAnalysisParams{
		Team:   []tools.Combatant{{Species: "a", Level: 40, FastMove: "FAST1"}},
		League: leagueGreat,
	})
	if !errors.Is(err, tools.ErrTeamSizeMismatch) {
		t.Errorf("error = %v, want wrapping ErrTeamSizeMismatch", err)
	}
}

func TestTeamAnalysisTool_NegativeTopN(t *testing.T) {
	t.Parallel()

	tool := newTeamAnalysisTool(t)
	handler := tool.Handler()

	valid := tools.Combatant{
		Species: "a", IV: [3]int{15, 15, 15}, Level: 40,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}

	_, _, err := handler(t.Context(), nil, tools.TeamAnalysisParams{
		Team:   []tools.Combatant{valid, valid, valid},
		League: leagueGreat,
		TopN:   -1,
	})
	if !errors.Is(err, tools.ErrInvalidTopN) {
		t.Errorf("error = %v, want wrapping ErrInvalidTopN", err)
	}
}

func TestTeamAnalysisTool_ZeroShieldsHonoured(t *testing.T) {
	t.Parallel()

	tool := newTeamAnalysisTool(t)
	handler := tool.Handler()

	valid := tools.Combatant{
		Species: "a", IV: [3]int{15, 15, 15}, Level: 40,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}
	team := []tools.Combatant{valid, valid, valid}

	_, withOneShield, err := handler(t.Context(), nil, tools.TeamAnalysisParams{
		Team: team, League: leagueGreat,
		Shields: []int{1, 1},
	})
	if err != nil {
		t.Fatalf("with 1 shield: %v", err)
	}

	_, withZeroShields, err := handler(t.Context(), nil, tools.TeamAnalysisParams{
		Team: team, League: leagueGreat,
		Shields: []int{0, 0},
	})
	if err != nil {
		t.Fatalf("with 0 shields: %v", err)
	}

	// Different shield counts must produce a different team_score;
	// if the two runs collapse onto the same aggregate the "shields=0
	// silently becomes 1" regression would be back.
	if withOneShield.TeamScore == withZeroShields.TeamScore {
		t.Errorf("team_score unchanged across shield counts (%.2f) — zero likely coerced to default",
			withOneShield.TeamScore)
	}
}

// TestTeamAnalysisTool_ChargedMovesEmptyIsJSONArray pins the
// wire-shape invariant: a team member with no charged moves must
// render as `"charged_moves": []`, not `"charged_moves": null`.
// The invariant exists because ResolvedCombatant (matchup /
// team_builder) and TeamMemberAnalysis (team_analysis) share a
// logical field and must marshal identically. Runs through the
// real handler so the bug would reappear if chargedMoveIDs ever
// reverts to returning nil on empty input.
func TestTeamAnalysisTool_ChargedMovesEmptyIsJSONArray(t *testing.T) {
	t.Parallel()

	tool := newTeamAnalysisTool(t)
	handler := tool.Handler()

	// FastMove explicit so applyMovesetDefaults does not auto-fill
	// charged moves from the rankings; ChargedMoves deliberately
	// empty so analyzeMember projects an empty engine slice.
	fastOnly := tools.Combatant{
		Species:  "a",
		IV:       [3]int{15, 15, 15},
		Level:    40,
		FastMove: "FAST1",
	}

	_, result, err := handler(t.Context(), nil, tools.TeamAnalysisParams{
		Team:   []tools.Combatant{fastOnly, fastOnly, fastOnly},
		League: leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.PerMember) == 0 {
		t.Fatal("PerMember is empty")
	}

	payload, err := json.Marshal(result.PerMember[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	decoded := map[string]any{}

	err = json.Unmarshal(payload, &decoded)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	value, present := decoded["charged_moves"]
	if !present {
		t.Fatal("charged_moves key missing from JSON output")
	}
	if value == nil {
		t.Errorf("charged_moves = null, want [] (empty array); raw=%s", payload)
	}
}

// TestTeamAnalysisTool_ScenariosAreAveraged asserts that passing
// multiple scenarios (Phase E semantics) averages the per-scenario
// ratings. A scenario list of [0, 2] must produce a team_score
// strictly between the pure [0] and pure [2] runs.
func TestTeamAnalysisTool_ScenariosAreAveraged(t *testing.T) {
	t.Parallel()

	tool := newTeamAnalysisTool(t)
	handler := tool.Handler()

	valid := tools.Combatant{
		Species: "a", IV: [3]int{15, 15, 15}, Level: 40,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}
	team := []tools.Combatant{valid, valid, valid}

	_, zero, err := handler(t.Context(), nil, tools.TeamAnalysisParams{
		Team: team, League: leagueGreat, Shields: []int{0},
	})
	if err != nil {
		t.Fatalf("[0]: %v", err)
	}

	_, two, err := handler(t.Context(), nil, tools.TeamAnalysisParams{
		Team: team, League: leagueGreat, Shields: []int{2},
	})
	if err != nil {
		t.Fatalf("[2]: %v", err)
	}

	_, mixed, err := handler(t.Context(), nil, tools.TeamAnalysisParams{
		Team: team, League: leagueGreat, Shields: []int{0, 2},
	})
	if err != nil {
		t.Fatalf("[0, 2]: %v", err)
	}

	minScore := min(zero.TeamScore, two.TeamScore)
	maxScore := max(zero.TeamScore, two.TeamScore)

	if mixed.TeamScore < minScore || mixed.TeamScore > maxScore {
		t.Errorf("mixed [0,2] team_score %.2f outside [%.2f, %.2f] — averaging is broken",
			mixed.TeamScore, minScore, maxScore)
	}
}

// TestTeamAnalysisTool_InvalidShieldsValue asserts that out-of-range
// shield values (> maxShieldCount) still fail cleanly under the new
// scenarios-list semantics. Phase E dropped the length==2 requirement
// — the per-entry range check remains the only rejection criterion.
func TestTeamAnalysisTool_InvalidShieldsValue(t *testing.T) {
	t.Parallel()

	tool := newTeamAnalysisTool(t)
	handler := tool.Handler()

	valid := tools.Combatant{
		Species: "a", IV: [3]int{15, 15, 15}, Level: 40,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}

	_, _, err := handler(t.Context(), nil, tools.TeamAnalysisParams{
		Team:    []tools.Combatant{valid, valid, valid},
		League:  leagueGreat,
		Shields: []int{3},
	})
	if !errors.Is(err, tools.ErrInvalidShields) {
		t.Errorf("error = %v, want wrapping ErrInvalidShields", err)
	}
}

func TestTeamAnalysisTool_UnknownLeague(t *testing.T) {
	t.Parallel()

	tool := newTeamAnalysisTool(t)
	handler := tool.Handler()

	valid := tools.Combatant{
		Species: "a", IV: [3]int{15, 15, 15}, Level: 40,
		FastMove: "FAST1", ChargedMoves: []string{"CH1"},
	}

	_, _, err := handler(t.Context(), nil, tools.TeamAnalysisParams{
		Team:   []tools.Combatant{valid, valid, valid},
		League: "marshmallow",
	})
	if !errors.Is(err, tools.ErrUnknownLeague) {
		t.Errorf("error = %v, want wrapping ErrUnknownLeague", err)
	}
}
