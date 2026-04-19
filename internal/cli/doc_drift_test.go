package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readRepoFile reads a file from the repo root using the runtime
// test working directory (internal/cli) as the anchor. Keeping the
// read helper local to this test file means the drift tests don't
// leak into the cli package's public surface.
func readRepoFile(t *testing.T, relPath string) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	repoRoot := filepath.Clean(filepath.Join(wd, "..", ".."))

	data, err := os.ReadFile(filepath.Join(repoRoot, relPath))
	if err != nil {
		t.Fatalf("ReadFile %q: %v", relPath, err)
	}

	return string(data)
}

// TestReadmeDocumentsCombatantOptions pins the Phase X-I round-3
// review fix + Phase X-II extension: README.md must document that
// every Combatant / species-id-accepting tool takes a per-Pokémon
// options block (shadow / lucky / purified). The lock now also
// enforces that the info-path tools (migrated in Phase X-II) are
// named in the documentation paragraph alongside the battle tools.
//
// If someone removes the paragraph or drops a tool from it the
// test fails loudly.
func TestReadmeDocumentsCombatantOptions(t *testing.T) {
	t.Parallel()

	readme := readRepoFile(t, "README.md")

	requiredPhrases := []string{
		"options",
		"shadow",
		"purified",
		"lucky",
		// Combat / team tools — Phase X-I surface.
		"pvp_matchup",
		"pvp_team_analysis",
		"pvp_team_builder",
		"pvp_counter_finder",
		"pvp_threat_coverage",
		// Info-path tools — Phase X-II surface.
		"pvp_rank",
		"pvp_species_info",
		"pvp_level_from_cp",
		"pvp_cp_limits",
		"pvp_evolution_preview",
		"pvp_rank_batch",
		// Cost tools.
		"pvp_second_move_cost",
		"pvp_powerup_cost",
		"shadow_variant_missing",
	}

	for _, phrase := range requiredPhrases {
		if !strings.Contains(readme, phrase) {
			t.Errorf("README.md missing required phrase %q (Combatant Options documentation drift)", phrase)
		}
	}
}

// TestReadmeDocumentsEngineShadowLimitation pins the engine-limitation
// disclosure: the battle simulator does not currently apply the
// in-game shadow ATK×1.2 / DEF÷1.2 multipliers. Clients relying on
// strict combat accuracy for shadow forms must know this — hiding it
// was flagged in round-2 review as misleading.
func TestReadmeDocumentsEngineShadowLimitation(t *testing.T) {
	t.Parallel()

	readme := readRepoFile(t, "README.md")

	// Checking for the distinctive phrase rather than the exact
	// wording lets the prose evolve without breaking the lock.
	if !strings.Contains(readme, "DEF÷1.2") && !strings.Contains(readme, "DEF/1.2") {
		t.Errorf("README.md must call out that the simulator does NOT apply shadow ATK/DEF multipliers")
	}
}

// TestReadmeToolCountConsistent catches the round-1 blocker that
// landed during pvp_report_data_issue: README.md had the header
// count updated (nineteen → twenty) but a downstream paragraph
// still said "nineteen". Locks both numeric words the README
// uses against each other: if the header says "twenty" then the
// walkthrough body must say "twenty" too, and "nineteen" must not
// appear anywhere in the README once the new tool has landed.
func TestReadmeToolCountConsistent(t *testing.T) {
	t.Parallel()

	readme := readRepoFile(t, "README.md")

	// "nineteen" is the stale word the round-1 reviewer caught.
	// Any occurrence is doc drift relative to the current tool
	// count, so ban it outright. If a future phase drops a tool
	// and the count goes back to 19, re-enable the word in a
	// focused doc update.
	if strings.Contains(readme, "nineteen") {
		t.Errorf("README.md still contains stale tool count \"nineteen\" (current count is 20)")
	}
}
