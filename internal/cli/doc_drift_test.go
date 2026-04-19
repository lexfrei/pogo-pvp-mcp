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

// TestReadmeDocumentsShadowMultipliers pins the Phase R4.7 status:
// the README must document that the battle simulator APPLIES the
// in-game shadow ATK × 1.2 / DEF ÷ 1.2 multipliers. Earlier rounds
// pinned the opposite ("does NOT yet apply"); this lock catches a
// regression in either direction — if the multipliers are ever
// silently removed, the README claim becomes misleading and this
// test fires.
func TestReadmeDocumentsShadowMultipliers(t *testing.T) {
	t.Parallel()

	readme := readRepoFile(t, "README.md")

	if !strings.Contains(readme, "ATK×1.2") && !strings.Contains(readme, "ATK x 1.2") {
		t.Errorf("README.md must document that the simulator applies shadow ATK × 1.2 multipliers")
	}

	if !strings.Contains(readme, "DEF÷1.2") && !strings.Contains(readme, "DEF/1.2") {
		t.Errorf("README.md must document that the simulator applies shadow DEF ÷ 1.2 multipliers")
	}

	// Stale phrasing from pre-R4.7 wording — catches a regression
	// that re-disclaims the multipliers as unimplemented.
	stale := []string{
		"does NOT yet apply in-game shadow",
		"does not currently apply the in-game shadow",
	}
	for _, phrase := range stale {
		if strings.Contains(readme, phrase) {
			t.Errorf("README.md still carries stale pre-R4.7 disclaimer %q; simulator now applies the multipliers",
				phrase)
		}
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

// TestReadmeDocumentsMCPHTTPListener pins the Phase 1 (public HTTP
// transport) README section: the opt-in env var, the Streamable HTTP
// nature, the separation from the loopback debug server, and the
// warning that middleware (rate-limit / max-body / per-call timeout)
// lands in later phases so Phase 1 must only be exposed behind a
// trusted proxy. All three are load-bearing pieces of operator
// guidance — dropping any one leaves a future reader with a
// dangerously incomplete picture.
func TestReadmeDocumentsMCPHTTPListener(t *testing.T) {
	t.Parallel()

	readme := readRepoFile(t, "README.md")

	requiredPhrases := []string{
		"POGO_PVP_SERVER_MCP_HTTP_LISTEN",
		"Streamable HTTP",
		"Phase 1 ships without rate-limit",
		"trusted reverse proxy",
	}

	for _, phrase := range requiredPhrases {
		if !strings.Contains(readme, phrase) {
			t.Errorf("README.md missing required phrase %q (Phase 1 MCP HTTP doc drift)", phrase)
		}
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

// TestCPCapJSONSchemaTagsMatch pins the same sibling invariant for
// the cp_cap override: pvp_rank and pvp_rank_batch both accept the
// override and route it through resolveCPCap with identical
// semantics. Their jsonschema tags MUST carry the same description
// so MCP clients don't see drift between two tools that behave
// identically.
func TestCPCapJSONSchemaTagsMatch(t *testing.T) {
	t.Parallel()

	const canonical = `"override (0 = league default); optimal level re-searched under the override"`

	for _, path := range []string{
		"internal/tools/rank.go",
		"internal/tools/rank_batch.go",
	} {
		src := readRepoFile(t, path)
		if !strings.Contains(src, canonical) {
			t.Errorf("%s missing canonical cp_cap jsonschema %s (drift between sibling tool schemas)",
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
