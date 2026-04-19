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
// review fix: README.md must explicitly document that the battle /
// team tools accept a per-Pokémon options block (shadow / lucky /
// purified). Before the fix the README only mentioned options on the
// pvp_second_move_cost bullet, leaving a user with no way to
// discover that matchup / team_analysis / team_builder /
// counter_finder / threat_coverage were also migrated.
//
// The lock tests against the generic paragraph text and the tool
// names the paragraph must enumerate. If someone removes the
// paragraph or drops a tool from it the test fails loudly.
func TestReadmeDocumentsCombatantOptions(t *testing.T) {
	t.Parallel()

	readme := readRepoFile(t, "README.md")

	requiredPhrases := []string{
		"options",
		"shadow",
		"purified",
		"lucky",
		"pvp_matchup",
		"pvp_team_analysis",
		"pvp_team_builder",
		"pvp_counter_finder",
		"pvp_threat_coverage",
		"shadow_variant_missing",
	}

	for _, phrase := range requiredPhrases {
		if !strings.Contains(readme, phrase) {
			t.Errorf("README.md missing required phrase %q (Combatant Options documentation drift)", phrase)
		}
	}

	if !strings.Contains(readme, "Phase X-II") {
		t.Errorf("README.md must acknowledge that info-path tools have NOT migrated to Options yet (Phase X-II follow-up)")
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
