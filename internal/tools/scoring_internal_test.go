package tools

import (
	"testing"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
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
