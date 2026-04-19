package tools_test

import (
	"encoding/json"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

// noopOptions carries every Options flag that is documented as
// no-op on the info-path tools (Lucky, Purified) so the assertion
// is "baseline response == response-with-flags". A single struct
// drives every tool-specific test below for consistency.
var noopOptions = tools.CombatantOptions{Lucky: true, Purified: true}

// marshalJSON returns the JSON serialisation of v as a string. Used
// by the no-op lock tests to compare entire response bodies without
// field-by-field comparison helpers — any accidental load-bearing
// wiring of Lucky / Purified into the info-path tools will surface
// as a JSON diff.
func marshalJSON(t *testing.T, v any) string {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	return string(data)
}

// TestRankTool_LuckyPurifiedAreNoOp pins Phase X-II round-1 review
// blocker: RankParams.Options.Lucky / Purified are documented as
// no-op on pvp_rank (stat product / CP / percent-of-best are all
// shadow/purified-independent; lucky is stardust-only). Lock the
// invariant with a baseline-vs-flagged JSON equality test so a
// future refactor accidentally wiring the flags into the rank
// pipeline is caught.
func TestRankTool_LuckyPurifiedAreNoOp(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, rankFixtureGamemaster)
	handler := tools.NewRankTool(mgr, nil).Handler()

	base := tools.RankParams{
		Species: speciesMedicham,
		IV:      [3]int{0, 15, 15},
		League:  leagueGreat,
	}

	_, baseline, err := handler(t.Context(), nil, base)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	withFlags := base
	withFlags.Options = noopOptions

	_, flagged, err := handler(t.Context(), nil, withFlags)
	if err != nil {
		t.Fatalf("flagged: %v", err)
	}

	if marshalJSON(t, baseline) != marshalJSON(t, flagged) {
		t.Errorf("Lucky/Purified must be no-op on pvp_rank; diff:\n  baseline=%s\n  flagged=%s",
			marshalJSON(t, baseline), marshalJSON(t, flagged))
	}
}

// TestCPLimitsTool_LuckyPurifiedAreNoOp pins the no-op contract
// for pvp_cp_limits: Lucky / Purified must not affect any league
// row. CP math is stat-driven, and pvpoke publishes shadow rows
// with identical BaseStats, so even Shadow is a lookup redirect
// rather than a multiplier on this path.
func TestCPLimitsTool_LuckyPurifiedAreNoOp(t *testing.T) {
	t.Parallel()

	mgr := newCPLimitsTestManager(t)
	handler := tools.NewCPLimitsTool(mgr).Handler()

	base := tools.CPLimitsParams{
		Species: speciesMedicham,
		IV:      [3]int{15, 15, 15},
	}

	_, baseline, err := handler(t.Context(), nil, base)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	withFlags := base
	withFlags.Options = noopOptions

	_, flagged, err := handler(t.Context(), nil, withFlags)
	if err != nil {
		t.Fatalf("flagged: %v", err)
	}

	if marshalJSON(t, baseline) != marshalJSON(t, flagged) {
		t.Errorf("Lucky/Purified must be no-op on pvp_cp_limits; diff:\n  baseline=%s\n  flagged=%s",
			marshalJSON(t, baseline), marshalJSON(t, flagged))
	}
}

// TestSpeciesInfoTool_LuckyPurifiedAreNoOp pins the no-op contract
// for pvp_species_info: Lucky / Purified must not alter stats,
// move lists, legacy flags, evolutions, or any other surfaced
// field. The response is purely a projection of the gamemaster
// entry.
func TestSpeciesInfoTool_LuckyPurifiedAreNoOp(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, infoFixtureGamemaster)
	handler := tools.NewSpeciesInfoTool(mgr, nil).Handler()

	base := tools.SpeciesInfoParams{Species: speciesMedicham}

	_, baseline, err := handler(t.Context(), nil, base)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	withFlags := base
	withFlags.Options = noopOptions

	_, flagged, err := handler(t.Context(), nil, withFlags)
	if err != nil {
		t.Fatalf("flagged: %v", err)
	}

	if marshalJSON(t, baseline) != marshalJSON(t, flagged) {
		t.Errorf("Lucky/Purified must be no-op on pvp_species_info; diff:\n  baseline=%s\n  flagged=%s",
			marshalJSON(t, baseline), marshalJSON(t, flagged))
	}
}

// TestEvolutionPreview_LuckyPurifiedAreNoOp pins the no-op
// contract for pvp_evolution_preview: Lucky / Purified must not
// affect the inverted level, the base species CP, or any
// descendant's stats.
func TestEvolutionPreview_LuckyPurifiedAreNoOp(t *testing.T) {
	t.Parallel()

	tool := newEvolutionPreviewTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	base := tools.EvolutionPreviewParams{
		Species: "squirtle",
		IV:      [3]int{15, 15, 15},
		CP:      1500,
		XL:      true,
	}

	_, baseline, err := handler(t.Context(), nil, base)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	withFlags := base
	withFlags.Options = noopOptions

	_, flagged, err := handler(t.Context(), nil, withFlags)
	if err != nil {
		t.Fatalf("flagged: %v", err)
	}

	if marshalJSON(t, baseline) != marshalJSON(t, flagged) {
		t.Errorf("Lucky/Purified must be no-op on pvp_evolution_preview; diff:\n  baseline=%s\n  flagged=%s",
			marshalJSON(t, baseline), marshalJSON(t, flagged))
	}
}

// TestRankBatch_LuckyPurifiedAreNoOp pins the no-op contract for
// pvp_rank_batch: because per-entry RankResult's no-op invariant
// already holds (locked by TestRankTool_LuckyPurifiedAreNoOp), the
// batch response must also be unchanged — Options is propagated
// unchanged to every per-IV RankParams, so any drift would be
// proportional to the batch size.
func TestRankBatch_LuckyPurifiedAreNoOp(t *testing.T) {
	t.Parallel()

	tool := newRankBatchTool(t)
	handler := tool.Handler()

	base := tools.RankBatchParams{
		Species: speciesMedicham,
		IVs:     [][3]int{{0, 15, 15}, {15, 15, 15}},
		League:  leagueGreat,
	}

	_, baseline, err := handler(t.Context(), nil, base)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	withFlags := base
	withFlags.Options = noopOptions

	_, flagged, err := handler(t.Context(), nil, withFlags)
	if err != nil {
		t.Fatalf("flagged: %v", err)
	}

	if marshalJSON(t, baseline) != marshalJSON(t, flagged) {
		t.Errorf("Lucky/Purified must be no-op on pvp_rank_batch; diff:\n  baseline=%s\n  flagged=%s",
			marshalJSON(t, baseline), marshalJSON(t, flagged))
	}
}

// TestRankTool_ShadowMissingFallback pins Phase X-II round-1 review
// blocker: Options.Shadow=true with a fixture that does NOT publish
// the "_shadow" row must fall back to the base species with
// ShadowVariantMissing=true. rank_batch's converse is locked
// separately; rank itself needs its own lock because each tool's
// fallback path assembles its response fields from the base
// species independently.
func TestRankTool_ShadowMissingFallback(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, rankFixtureGamemaster)
	handler := tools.NewRankTool(mgr, nil).Handler()

	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: speciesMedicham,
		IV:      [3]int{15, 15, 15},
		League:  leagueGreat,
		Options: tools.CombatantOptions{Shadow: true},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.ResolvedSpeciesID != speciesMedicham {
		t.Errorf("ResolvedSpeciesID = %q, want %q (fallback to base)",
			result.ResolvedSpeciesID, speciesMedicham)
	}

	if !result.ShadowVariantMissing {
		t.Errorf("ShadowVariantMissing = false; fixture does not publish _shadow — must signal missing")
	}
}

// TestCPLimitsTool_ShadowMissingFallback pins the same converse
// path on pvp_cp_limits.
func TestCPLimitsTool_ShadowMissingFallback(t *testing.T) {
	t.Parallel()

	mgr := newCPLimitsTestManager(t)
	handler := tools.NewCPLimitsTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.CPLimitsParams{
		Species: speciesMedicham,
		IV:      [3]int{15, 15, 15},
		Options: tools.CombatantOptions{Shadow: true},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.ResolvedSpeciesID != speciesMedicham {
		t.Errorf("ResolvedSpeciesID = %q, want %q (fallback to base)",
			result.ResolvedSpeciesID, speciesMedicham)
	}

	if !result.ShadowVariantMissing {
		t.Errorf("ShadowVariantMissing = false; fixture does not publish _shadow — must signal missing")
	}
}

// TestSpeciesInfoTool_ShadowMissingFallback pins the converse on
// pvp_species_info: the fallback path must surface the BASE
// species' move lists / legacy list / evolutions unchanged, with
// ShadowVariantMissing=true signalling that the shadow-specific
// content (which may diverge in pvpoke) was unavailable.
func TestSpeciesInfoTool_ShadowMissingFallback(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, infoFixtureGamemaster)
	handler := tools.NewSpeciesInfoTool(mgr, nil).Handler()

	_, result, err := handler(t.Context(), nil, tools.SpeciesInfoParams{
		Species: speciesMedicham,
		Options: tools.CombatantOptions{Shadow: true},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.ResolvedSpeciesID != speciesMedicham {
		t.Errorf("ResolvedSpeciesID = %q, want %q (fallback to base)",
			result.ResolvedSpeciesID, speciesMedicham)
	}

	if !result.ShadowVariantMissing {
		t.Errorf("ShadowVariantMissing = false; fixture does not publish _shadow — must signal missing")
	}
}

// TestEvolutionPreview_ShadowMissingFallback pins the converse on
// pvp_evolution_preview: the fallback walks the BASE species'
// Evolutions chain while surfacing ShadowVariantMissing=true.
func TestEvolutionPreview_ShadowMissingFallback(t *testing.T) {
	t.Parallel()

	tool := newEvolutionPreviewTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionPreviewParams{
		Species: "squirtle",
		IV:      [3]int{15, 15, 15},
		CP:      1500,
		XL:      true,
		Options: tools.CombatantOptions{Shadow: true},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.ResolvedSpeciesID != "squirtle" {
		t.Errorf("ResolvedSpeciesID = %q, want %q (fallback to base)",
			result.ResolvedSpeciesID, "squirtle")
	}

	if !result.ShadowVariantMissing {
		t.Errorf("ShadowVariantMissing = false; fixture does not publish _shadow — must signal missing")
	}
}
