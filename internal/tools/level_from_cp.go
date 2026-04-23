package tools

import (
	"context"
	"fmt"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// LevelFromCPParams is the JSON input contract for pvp_level_from_cp.
// Shadow variants are addressed by setting Options.Shadow=true; the
// legacy "medicham_shadow" suffix is still tolerated for backward
// compatibility (see resolveSpeciesLookup's TrimSuffix). Lucky /
// Purified are accepted for Options-struct symmetry across tools
// but have no effect on CP inversion math. XL allows levels above
// NoXLMaxLevel (40); default false.
type LevelFromCPParams struct {
	Species string           `json:"species" jsonschema:"species id in the pvpoke gamemaster"`
	IV      [3]int           `json:"iv" jsonschema:"individual values [atk, def, sta]; each 0..15"`
	CP      int              `json:"cp" jsonschema:"the observed CP to invert back into a level"`
	XL      bool             `json:"xl,omitempty" jsonschema:"allow XL candy levels above 40"`
	Options CombatantOptions `json:"options,omitzero" jsonschema:"shadow / lucky / purified flags; only Shadow is load-bearing here"`
}

// LevelFromCPResult reports the highest level (on the 0.5 grid) at
// which (species, iv) reaches CP ≤ the requested target. Exact is
// true when the level's CP equals the requested CP; otherwise the
// returned level is the greatest one that still fits under target.
// Reachable is false when the requested CP is BEYOND what the given
// IV spread can ever reach at this species' level cap (i.e. the
// inversion clamped at MaxLevel and the resulting CP is still below
// the requested target) — lets the caller distinguish "off-grid,
// nearest level picked" (reachable=true, exact=false) from "CP
// target is impossible for this IV spread under the XL flag"
// (reachable=false). ResolvedSpeciesID / ShadowVariantMissing echo
// the shadow-aware lookup (see resolveSpeciesLookup).
type LevelFromCPResult struct {
	Species              string  `json:"species"`
	ResolvedSpeciesID    string  `json:"resolved_species_id,omitempty"`
	IV                   [3]int  `json:"iv"`
	Level                float64 `json:"level"`
	Exact                bool    `json:"exact"`
	Reachable            bool    `json:"reachable"`
	CP                   int     `json:"cp"`
	Atk                  float64 `json:"atk"`
	Def                  float64 `json:"def"`
	HP                   int     `json:"hp"`
	StatProduct          float64 `json:"stat_product"`
	ShadowVariantMissing bool    `json:"shadow_variant_missing,omitempty"`
}

// LevelFromCPTool is a thin wrapper around pogopvp.LevelForCP. The
// resolved level is re-evaluated against the species stat line to
// fill Atk / Def / HP / StatProduct — the usual post-resolution
// bundle the client expects, matching pvp_rank's shape.
type LevelFromCPTool struct {
	manager *gamemaster.Manager
}

// NewLevelFromCPTool constructs the tool bound to the gamemaster.
func NewLevelFromCPTool(manager *gamemaster.Manager) *LevelFromCPTool {
	return &LevelFromCPTool{manager: manager}
}

const levelFromCPToolDescription = "Given species + IVs + an observed CP, return the highest level (on the " +
	"0.5 grid) at which the Pokémon reaches CP ≤ the target. Useful for pinning down the level of a " +
	"wild catch before committing to a power-up budget."

// Tool returns the MCP registration.
func (tool *LevelFromCPTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_level_from_cp",
		Description: levelFromCPToolDescription,
	}
}

// Handler returns the MCP-typed handler.
func (tool *LevelFromCPTool) Handler() mcp.ToolHandlerFor[LevelFromCPParams, LevelFromCPResult] {
	return tool.handle
}

// handle orchestrates the inverse search: resolves species, builds
// an IV, calls engine LevelForCP, then re-evaluates Stats at the
// returned level so the response carries the same post-resolution
// bundle as pvp_rank.
func (tool *LevelFromCPTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params LevelFromCPParams,
) (*mcp.CallToolResult, LevelFromCPResult, error) {
	err := ctx.Err()
	if err != nil {
		return nil, LevelFromCPResult{}, fmt.Errorf("level_from_cp cancelled: %w", err)
	}

	snapshot := tool.manager.Current()
	if snapshot == nil {
		return nil, LevelFromCPResult{}, ErrGamemasterNotLoaded
	}

	species, resolvedID, shadowMissing, ok := resolveSpeciesLookup(snapshot, params.Species, params.Options)
	if !ok {
		return nil, LevelFromCPResult{}, fmt.Errorf("%w: %q", ErrUnknownSpecies, params.Species)
	}

	ivs, err := pogopvp.NewIV(params.IV[0], params.IV[1], params.IV[2])
	if err != nil {
		return nil, LevelFromCPResult{}, fmt.Errorf("invalid IV: %w", err)
	}

	result, err := pogopvp.LevelForCP(species.BaseStats, ivs, params.CP,
		pogopvp.FindSpreadOpts{XLAllowed: params.XL})
	if err != nil {
		return nil, LevelFromCPResult{}, fmt.Errorf("level_for_cp: %w", err)
	}

	out, err := buildLevelFromCPResult(species, ivs, &params, resolvedID, shadowMissing, &result)
	if err != nil {
		return nil, LevelFromCPResult{}, err
	}

	return nil, out, nil
}

// buildLevelFromCPResult re-evaluates stats at the resolved level
// and assembles the response bundle. Factored out so handle() stays
// under funlen.
func buildLevelFromCPResult(
	species pogopvp.Species, ivs pogopvp.IV,
	params *LevelFromCPParams, resolvedID string, shadowMissing bool,
	result *pogopvp.LevelResult,
) (LevelFromCPResult, error) {
	cpm, err := pogopvp.CPMAt(result.Level)
	if err != nil {
		return LevelFromCPResult{}, fmt.Errorf("cpm at level %.1f: %w", result.Level, err)
	}

	stats := pogopvp.ComputeStats(species.BaseStats, ivs, cpm)

	return LevelFromCPResult{
		Species:              params.Species,
		ResolvedSpeciesID:    resolvedID,
		IV:                   params.IV,
		Level:                result.Level,
		Exact:                result.Exact,
		Reachable:            reachableCP(result.Level, result.CP, params.CP, params.XL),
		CP:                   result.CP,
		Atk:                  stats.Atk,
		Def:                  stats.Def,
		HP:                   stats.HP,
		StatProduct:          pogopvp.ComputeStatProduct(stats),
		ShadowVariantMissing: shadowMissing,
	}, nil
}

// reachableCP decides whether the requested CP target is attainable
// for the species / IV spread. The inversion returns the greatest
// 0.5-grid level with CP ≤ target, so result.CP < target is the
// default case. Reachable is false ONLY when the level is pinned at
// the cap (MaxLevel with XL, NoXLMaxLevel without) AND the resulting
// CP is still short of the target — i.e. the IV spread tops out
// below what was asked. Every other shape (CP ≥ target, or level
// strictly below the cap with CP < target) means the target is
// reachable; in the second case the picked level is just the
// nearest-under-target on the 0.5 grid.
func reachableCP(level float64, resolvedCP, targetCP int, allowXL bool) bool {
	if resolvedCP >= targetCP {
		return true
	}

	maxLevel := pogopvp.NoXLMaxLevel
	if allowXL {
		maxLevel = pogopvp.MaxLevel
	}

	return level < maxLevel
}
