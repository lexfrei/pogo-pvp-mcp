package tools

import (
	"strings"
	"testing"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
)

// TestAverageRatingAcrossScenarios_EmptyReturnsFailure pins the
// Phase-review fix: an empty scenarios slice must return ok=false,
// not the 500 tie midpoint. Tested directly (white-box) because
// resolveTeamDefaults always supplies at least one scenario in
// practice, so the defensive branch is unreachable from the public
// handler — but the CLAUDE.md invariant against silent 500
// fallbacks applies regardless of caller discipline.
func TestAverageRatingAcrossScenarios_EmptyReturnsFailure(t *testing.T) {
	t.Parallel()

	// The combatants are never dereferenced on the empty-scenarios
	// branch, so zero-valued pointers suffice.
	var member, opponent pogopvp.Combatant

	rating, ok := averageRatingAcrossScenarios(&member, &opponent, nil)

	if ok {
		t.Errorf("ok = true, want false on empty scenarios")
	}
	if rating == ratingMidpoint {
		t.Errorf("rating = ratingMidpoint (%d), want 0 — Phase-review invariant bans 500 fallback",
			ratingMidpoint)
	}
}

// TestScoreTripleFromMatrix_AllFailuresReturnsFalse proves that the
// bool signal correctly distinguishes "no valid sample" (every
// scenario cell failed) from a legitimate 0 rating. Before Phase
// review the function returned a bare float64 and callers could not
// tell the two cases apart, inflating the Pareto overall mean.
func TestScoreTripleFromMatrix_AllFailuresReturnsFalse(t *testing.T) {
	t.Parallel()

	const poolSize = 3

	// 3-pool × 1-opponent matrix where every scenario cell is
	// a simulate failure (OK=false). The bool signal should say
	// "no valid sample".
	matrix := make([][][scenarioCount]ratingMatrixEntry, poolSize)
	for i := range matrix {
		matrix[i] = make([][scenarioCount]ratingMatrixEntry, 1)
		for scenario := range scenarioCount {
			matrix[i][0][scenario] = ratingMatrixEntry{Rating: 0, OK: false}
		}
	}

	score, ok := scoreTripleFromMatrix(matrix, 0, 1, 2, 0)

	if ok {
		t.Errorf("ok = true, want false when every cell OK=false")
	}
	if score != 0 {
		t.Errorf("score = %f, want 0 on all-failure path", score)
	}
}

// TestAverageScenarioScore_AllFailuresReturnsFalse pins the round-2
// review fix: when every shield scenario for a triple returns
// ok=false from scoreTripleFromMatrix, averageScenarioScore must
// propagate that signal (ok=false) to its caller so updateOverallBest
// does not promote a phantom 0-score team to "best overall". Before
// the fix the function returned bare float64 and dropped the signal
// at the boundary.
func TestAverageScenarioScore_AllFailuresReturnsFalse(t *testing.T) {
	t.Parallel()

	const poolSize = 3

	matrix := make([][][scenarioCount]ratingMatrixEntry, poolSize)
	for i := range matrix {
		matrix[i] = make([][scenarioCount]ratingMatrixEntry, 1)
		for scenario := range scenarioCount {
			matrix[i][0][scenario] = ratingMatrixEntry{Rating: 0, OK: false}
		}
	}

	score, ok := averageScenarioScore(matrix, 0, 1, 2)

	if ok {
		t.Errorf("ok = true, want false when every scenario is all-failures")
	}
	if score != 0 {
		t.Errorf("score = %f, want 0 on all-failure path", score)
	}
}

// TestScoreTripleFromMatrix_ZeroRatingIsValid pins the other side of
// the same invariant: a legitimate 0 rating (everyone lost with
// defender at full HP) must be reported with ok=true so downstream
// averaging counts the scenario instead of silently dropping it.
func TestScoreTripleFromMatrix_ZeroRatingIsValid(t *testing.T) {
	t.Parallel()

	const poolSize = 3

	matrix := make([][][scenarioCount]ratingMatrixEntry, poolSize)
	for i := range matrix {
		matrix[i] = make([][scenarioCount]ratingMatrixEntry, 1)
		for scenario := range scenarioCount {
			matrix[i][0][scenario] = ratingMatrixEntry{Rating: 0, OK: true}
		}
	}

	score, ok := scoreTripleFromMatrix(matrix, 0, 1, 2, 0)

	if !ok {
		t.Errorf("ok = false, want true when every cell OK=true (legitimate 0 score)")
	}
	if score != 0 {
		t.Errorf("score = %f, want 0 (every cell contributed a 0 rating)", score)
	}
}

// TestSimulateTeamMatrix_SinglePassPerCell pins the Phase-2B
// performance invariant: runTeamAnalysis must compute every
// (member, opp, scenario) cell exactly once. simulateTeamMatrix is
// the single call site for ratingFor, so proving the matrix has
// exactly team × meta × |scenarios| cells — each OK on a valid
// fixture — proves the total Simulate count has no 2× regression.
//
// Build combatants from scratch rather than going through the full
// handler: the test is about the count, not about the end-to-end
// plumbing.
func TestSimulateTeamMatrix_SinglePassPerCell(t *testing.T) {
	t.Parallel()

	ivs, err := pogopvp.NewIV(15, 15, 15)
	if err != nil {
		t.Fatalf("NewIV: %v", err)
	}

	species := pogopvp.Species{
		ID:        "alpha",
		BaseStats: pogopvp.BaseStats{Atk: 121, Def: 152, HP: 155},
	}
	fast := pogopvp.Move{
		ID: "FAST1", Category: pogopvp.MoveCategoryFast, Power: 3,
		EnergyGain: 5, Cooldown: 1000, Turns: 2,
	}
	charged := pogopvp.Move{
		ID: "CH1", Category: pogopvp.MoveCategoryCharged, Power: 50, Energy: 35,
	}

	build := func() pogopvp.Combatant {
		return pogopvp.Combatant{
			Species:      species,
			IV:           ivs,
			Level:        40,
			FastMove:     fast,
			ChargedMoves: []pogopvp.Move{charged},
			Shields:      1,
		}
	}

	team := []pogopvp.Combatant{build(), build(), build()}
	meta := []pogopvp.Combatant{build(), build()}
	scenarios := []int{0, 1, 2}

	matrix := simulateTeamMatrix(t.Context(), team, meta, scenarios)

	if len(matrix) != len(team) {
		t.Fatalf("matrix outer len = %d, want %d (team size)", len(matrix), len(team))
	}

	var (
		total int
		okCnt int
	)

	for i := range matrix {
		if len(matrix[i]) != len(meta) {
			t.Errorf("matrix[%d] len = %d, want %d (meta size)", i, len(matrix[i]), len(meta))

			continue
		}

		for j := range matrix[i] {
			if len(matrix[i][j]) != len(scenarios) {
				t.Errorf("matrix[%d][%d] len = %d, want %d (scenarios len)",
					i, j, len(matrix[i][j]), len(scenarios))

				continue
			}

			for _, cell := range matrix[i][j] {
				total++

				if cell.OK {
					okCnt++
				}
			}
		}
	}

	expected := len(team) * len(meta) * len(scenarios)

	if total != expected {
		t.Errorf("total cells = %d, want %d (team × meta × scenarios)", total, expected)
	}

	if okCnt != expected {
		t.Errorf("OK cells = %d, want %d — a valid fixture must produce every rating",
			okCnt, expected)
	}
}

// TestBuildOneMetaCombatant_IsShadowFromSuffix pins the Phase R4.7
// meta-path wiring: buildOneMetaCombatant sets Combatant.IsShadow
// true IFF entry.SpeciesID ends with the "_shadow" suffix. The
// pvpoke-published meta list encodes shadow forms this way, so the
// simulator must see IsShadow=true on those rows for the ATK × 1.2
// / DEF ÷ 1.2 adjustments to apply consistently across pvp_team_
// analysis, pvp_team_builder, pvp_counter_finder, pvp_threat_
// coverage, and the pvp_rank non-legacy scorer.
//
// White-box: reaches into the unexported helper directly. The
// end-to-end coverage in matchup_test.go exercises the attacker
// path; this test covers the meta path the public handler tests
// can't easily inspect per-entry.
func TestBuildOneMetaCombatant_IsShadowFromSuffix(t *testing.T) {
	t.Parallel()

	const fixtureGM = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 308, "speciesId": "medicham", "speciesName": "Medicham",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"], "released": true},
    {"dex": 308, "speciesId": "medicham_shadow", "speciesName": "Medicham (Shadow)",
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

	snapshot, err := pogopvp.ParseGamemaster(strings.NewReader(fixtureGM))
	if err != nil {
		t.Fatalf("ParseGamemaster: %v", err)
	}

	cases := []struct {
		name       string
		speciesID  string
		wantShadow bool
	}{
		{"non_shadow", "medicham", false},
		{"shadow_suffix", "medicham_shadow", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			entry := rankings.RankingEntry{
				SpeciesID: tc.speciesID,
				Moveset:   []string{"COUNTER", "ICE_PUNCH"},
			}

			combatant, buildErr := buildOneMetaCombatant(snapshot, &entry, 1500, 1)
			if buildErr != nil {
				t.Fatalf("buildOneMetaCombatant: %v", buildErr)
			}

			if combatant.IsShadow != tc.wantShadow {
				t.Errorf("IsShadow = %v, want %v for species_id %q",
					combatant.IsShadow, tc.wantShadow, tc.speciesID)
			}
		})
	}
}
