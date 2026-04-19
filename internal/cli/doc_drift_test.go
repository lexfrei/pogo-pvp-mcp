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

// TestReadmeToolCountConsistent catches the class of bug that
// recurs every time a new tool lands: the README header count gets
// bumped but a downstream walkthrough paragraph still names the
// old number. Instead of banning specific stale words one at a
// time (this caught "nineteen" and then "twenty"), the test pins
// that exactly one English number-word describing the tool count
// appears in README.md and that it matches the current tool
// count. Bump currentToolCount when a tool lands or drops.
func TestReadmeToolCountConsistent(t *testing.T) {
	t.Parallel()

	readme := readRepoFile(t, "README.md")

	const currentToolCount = "twenty-two"

	// Stale number-words from prior milestones. If a new tool
	// pushes the count past "twenty-two", add the retired word
	// here and bump currentToolCount above.
	stale := []string{
		"nineteen", "twenty MCP", "twenty `pvp_*`", "twenty currently",
		"twenty-one",
	}

	for _, word := range stale {
		if strings.Contains(readme, word) {
			t.Errorf("README.md still contains stale tool-count phrase %q (current count is %s)",
				word, currentToolCount)
		}
	}

	if !strings.Contains(readme, currentToolCount) {
		t.Errorf("README.md missing expected current tool-count word %q — header or walkthrough drift",
			currentToolCount)
	}
}

// TestReadmeDocumentsTargetLevelAndCPCapNuance pins the Phase R4.8
// doc-gap fix: the README must explicitly document the semantics of
// the `target_level` parameter (omit / 0 vs positive) on team tools
// and the `cp_cap` override on pvp_rank. Past sessions repeatedly hit
// ambiguity — callers defaulted `target_level: 0` expecting "no
// powerup" rather than "deepest fit under cap", and `cp_cap`
// overrides were undocumented. The test locks distinctive phrases so
// future rewrites can't silently drop them.
func TestReadmeDocumentsTargetLevelAndCPCapNuance(t *testing.T) {
	t.Parallel()

	readme := readRepoFile(t, "README.md")

	requiredPhrases := []string{
		"deepest level fitting the league CP cap",
		"already_at_or_above_target",
		"re-searches the optimal level under that cap",
	}

	for _, phrase := range requiredPhrases {
		if !strings.Contains(readme, phrase) {
			t.Errorf("README.md missing required phrase %q (target_level / cp_cap nuance doc drift)", phrase)
		}
	}
}

// TestTargetLevelJSONSchemaTagsMatch pins a sibling invariant of
// TestReadmeDocumentsTargetLevelAndCPCapNuance: team_analysis.go and
// team_builder.go both expose `target_level` on their params structs
// and both route through computeMemberCost with identical semantics.
// Their jsonschema tags MUST carry the same description — an MCP
// client reading the per-tool schema over the wire would otherwise
// see a drift between two tools that behave identically. This test
// loads both source files as raw text and checks that both contain
// the canonical description string.
func TestTargetLevelJSONSchemaTagsMatch(t *testing.T) {
	t.Parallel()

	const canonical = `"cost target; 0 = deepest fit under cap; positive = 0.5-grid level"`

	for _, path := range []string{
		"internal/tools/team_builder.go",
		"internal/tools/team_analysis.go",
	} {
		src := readRepoFile(t, path)
		if !strings.Contains(src, canonical) {
			t.Errorf("%s missing canonical target_level jsonschema %s (drift between sibling tool schemas)",
				path, canonical)
		}
	}
}

// TestReportDataIssueURLMatchesLiveRepo pins the round-2 fix for
// pvp_report_data_issue: the tool's outbound URLs must target the
// live GitHub repository name, not the Go module path. CLAUDE.md
// records the `gh repo rename` from `pvpoke-mcp` to `pogo-pvp-mcp`
// as pending; using the pending path in the URL produces a 404.
// When the rename lands, flip both the hardcoded URL in
// report_data_issue.go AND the "rename is pending" note in
// CLAUDE.md in one commit — this test asserts they stay in sync:
// if CLAUDE.md stops saying rename-is-pending, the URL must have
// flipped to pogo-pvp-mcp; while the note is still present, the
// URL must still point at pvpoke-mcp.
func TestReportDataIssueURLMatchesLiveRepo(t *testing.T) {
	t.Parallel()

	claudeMD := readRepoFile(t, "CLAUDE.md")

	reportTool := readRepoFile(t, "internal/tools/report_data_issue.go")

	renamePending := strings.Contains(claudeMD, "`gh repo rename` to `pogo-pvp-mcp` is pending")

	urlUsesPVPoke := strings.Contains(reportTool, `"https://github.com/lexfrei/pvpoke-mcp"`)
	urlUsesPogo := strings.Contains(reportTool, `"https://github.com/lexfrei/pogo-pvp-mcp"`)

	if renamePending && !urlUsesPVPoke {
		t.Errorf("CLAUDE.md says the rename is pending but report_data_issue.go does not use the pvpoke-mcp URL")
	}

	if !renamePending && !urlUsesPogo {
		t.Errorf("CLAUDE.md no longer flags the rename as pending but report_data_issue.go does not use the pogo-pvp-mcp URL")
	}
}
