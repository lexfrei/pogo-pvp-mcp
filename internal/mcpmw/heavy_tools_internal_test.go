package mcpmw

import "testing"

// TestHeavyToolsDriftGuard pins the exact contents of heavyTools so
// a casual refactor (adding a new sweep-style tool to the server,
// renaming one, accidentally dropping one) fails here instead of
// silently shipping with the wrong budget tier. Keep in sync with
// README "heavy methods" list and CLAUDE.md.
func TestHeavyToolsDriftGuard(t *testing.T) {
	t.Parallel()

	want := []string{
		"pvp_team_builder",
		"pvp_team_analysis",
		"pvp_threat_coverage",
		"pvp_counter_finder",
		"pvp_rank_batch",
	}

	if len(heavyTools) != len(want) {
		t.Errorf("len(heavyTools) = %d, want %d — add/remove README entry too",
			len(heavyTools), len(want))
	}

	for _, name := range want {
		if _, ok := heavyTools[name]; !ok {
			t.Errorf("heavyTools missing %q", name)
		}
	}
}
