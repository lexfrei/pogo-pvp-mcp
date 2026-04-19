package tools

import (
	"context"
	"fmt"
	"strings"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SecondMoveCostParams is the JSON input for pvp_second_move_cost.
// Options carries the shadow / lucky / purified flags (Phase X
// refactor). Lucky has no effect on second-move cost (Niantic's
// 50% stardust discount is powerup-only). Shadow applies ×1.2 to
// both stardust and candy. Purified applies ×0.9 to both. Flags
// stack per Bulbapedia (lucky+purified yields 0.5×0.9 on stardust,
// etc.), though the only stacks meaningful here are purified-only.
type SecondMoveCostParams struct {
	Species string           `json:"species" jsonschema:"species id; set Options.Shadow=true for shadow form"`
	Options CombatantOptions `json:"options,omitzero" jsonschema:"shadow / lucky / purified flags"`
}

// SecondMoveCostResult is the JSON output. Pokémon GO charges
// stardust AND candy to unlock a second charged move. pvpoke's
// gamemaster only carries the stardust number; the candy cost is
// derived from the species' buddy distance using Niantic's
// canonical table (1km → 25 candy, 3km → 50, 5km → 75, 20km → 100).
//
// StardustCost / CandyCost are the already-multiplied values (shadow
// species pay 1.2× both currencies). CostMultiplier carries the
// applied factor so callers can back it out if they need the
// non-shadow baseline. Purification has its own Niantic-published
// discount on both currencies, but pvpoke does not expose a
// purified species id and this tool does not model it — callers
// handling purified forms must consult Niantic's current rate
// table directly rather than inferring from the shadow multiplier.
//
// CandyCostAvailable reports whether the candy derivation
// succeeded: false means the gamemaster does not publish a
// buddy distance for this species (no derivation possible), in
// which case CandyCost is zero. Callers must check the flag
// before acting on CandyCost — zero is not a valid Pokémon GO
// second-move candy cost.
type SecondMoveCostResult struct {
	Species               string  `json:"species"`
	ResolvedSpeciesID     string  `json:"resolved_species_id,omitempty"`
	StardustCost          int     `json:"stardust_cost"`
	CandyCost             int     `json:"candy_cost"`
	BuddyDistanceKM       int     `json:"buddy_distance_km,omitempty"`
	CandyCostAvailable    bool    `json:"candy_cost_available"`
	StardustCostAvailable bool    `json:"stardust_cost_available"`
	CostMultiplier        float64 `json:"cost_multiplier"`
	ShadowVariantMissing  bool    `json:"shadow_variant_missing,omitempty"`
	Note                  string  `json:"note,omitempty"`
}

// SecondMoveCostTool wraps the gamemaster manager.
type SecondMoveCostTool struct {
	gm *gamemaster.Manager
}

// NewSecondMoveCostTool constructs the tool.
func NewSecondMoveCostTool(gm *gamemaster.Manager) *SecondMoveCostTool {
	return &SecondMoveCostTool{gm: gm}
}

const secondMoveCostToolDescription = "Given a species id, return the Pokémon GO cost (stardust + candy) to " +
	"unlock a second charged move slot. Stardust is read from the gamemaster's thirdMoveCost field; candy is " +
	"derived from the species' buddy distance (1km=25, 3km=50, 5km=75, 20km=100). Set options.shadow=true for " +
	"shadow form (×1.2 stardust + candy) or options.purified=true for purified form (×0.9 both); lucky has no " +
	"effect here (Niantic's 50% discount is powerup-only). Flags stack. Zero fields with availability=false " +
	"signal the upstream data is missing."

// Tool returns the MCP tool registration.
func (*SecondMoveCostTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_second_move_cost",
		Description: secondMoveCostToolDescription,
	}
}

// Handler returns the MCP-typed handler function.
func (tool *SecondMoveCostTool) Handler() mcp.ToolHandlerFor[SecondMoveCostParams, SecondMoveCostResult] {
	return tool.handle
}

// handle looks up the species, derives the candy cost from its
// buddy distance, and applies shadow / purified multipliers from
// params.Options.
func (tool *SecondMoveCostTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params SecondMoveCostParams,
) (*mcp.CallToolResult, SecondMoveCostResult, error) {
	err := ctx.Err()
	if err != nil {
		return nil, SecondMoveCostResult{}, fmt.Errorf("second_move_cost cancelled: %w", err)
	}

	snapshot := tool.gm.Current()
	if snapshot == nil {
		return nil, SecondMoveCostResult{}, ErrGamemasterNotLoaded
	}

	if params.Species == "" {
		return nil, SecondMoveCostResult{}, fmt.Errorf("%w: species required", ErrUnknownSpecies)
	}

	species, resolvedID, shadowMissing, ok := resolveSpeciesLookup(
		snapshot, params.Species, params.Options)
	if !ok {
		return nil, SecondMoveCostResult{}, fmt.Errorf("%w: %q", ErrUnknownSpecies, params.Species)
	}

	return nil, buildSecondMoveCostResult(
		&species, params, resolvedID, shadowMissing), nil
}

// buildSecondMoveCostResult applies modifier multipliers and packs
// the response. Split out of handle to keep funlen under budget.
func buildSecondMoveCostResult(
	species *pogopvp.Species, params SecondMoveCostParams,
	resolvedID string, shadowMissing bool,
) SecondMoveCostResult {
	stardust := species.ThirdMoveCost
	candy, candyOK := candyCostFromBuddy(species.BuddyDistance)

	multiplier := costMultiplierFor(params.Options)
	stardust = scaleCost(stardust, multiplier)

	if candyOK {
		candy = scaleCost(candy, multiplier)
	}

	return SecondMoveCostResult{
		Species:               params.Species,
		ResolvedSpeciesID:     resolvedID,
		StardustCost:          stardust,
		CandyCost:             candy,
		BuddyDistanceKM:       species.BuddyDistance,
		StardustCostAvailable: species.ThirdMoveCost > 0,
		CandyCostAvailable:    candyOK,
		CostMultiplier:        multiplier,
		ShadowVariantMissing:  shadowMissing,
		Note: buildSecondMoveCostNote(
			species.ThirdMoveCost, candyOK, params.Options),
	}
}

// costMultiplierFor returns the stacked multiplier for stardust +
// candy on second-move unlock. Lucky does NOT apply here
// (Bulbapedia: lucky is powerup-stardust only). Shadow ×1.2,
// purified ×0.9, stackable but shadow+purified combination is not
// a real in-game state (shadow must be purified to become purified,
// losing the shadow marker in the process) — we still honour the
// flags as the caller sent them.
func costMultiplierFor(opts CombatantOptions) float64 {
	m := 1.0
	if opts.Shadow {
		m *= shadowCostMultiplier
	}

	if opts.Purified {
		m *= purifiedCostMultiplier
	}

	return m
}

// scaleCost applies the multiplier via integer arithmetic where
// possible (1.2 → ×12/÷10, 0.9 → ×9/÷10) to avoid float-rounding
// drift in JSON output. 1.0 short-circuits to the input.
func scaleCost(cost int, multiplier float64) int {
	switch multiplier {
	case 1.0:
		return cost
	case shadowCostMultiplier: // 1.2
		return cost * shadowMultiplierNumerator / costMultiplierDenominator
	case purifiedCostMultiplier: // 0.9
		return cost * purifiedMultiplierNumerator / costMultiplierDenominator
	case shadowCostMultiplier * purifiedCostMultiplier: // 1.08
		return cost * shadowMultiplierNumerator * purifiedMultiplierNumerator /
			(costMultiplierDenominator * costMultiplierDenominator)
	default:
		// Exotic combinations not in current factor set — approximate
		// through float, round to nearest; today this branch is
		// unreachable with the 2-flag set but keeps the function total.
		return int(float64(cost)*multiplier + floatRoundHalfUp)
	}
}

// Multiplier-factor constants kept as integer pairs so scaleCost
// can preserve round-trip exactness on the published cost tables.
const (
	shadowCostMultiplier        = 1.2
	purifiedCostMultiplier      = 0.9
	shadowMultiplierNumerator   = 12
	purifiedMultiplierNumerator = 9
	costMultiplierDenominator   = 10
	floatRoundHalfUp            = 0.5
)

// Canonical Pokémon GO buddy-distance brackets + their associated
// candy cost to unlock a second charged move. Update when Niantic
// shifts a mechanic (the 2024 legendary rework pinned some
// legendaries at 25 candy regardless of buddy distance — not
// modelled here).
const (
	buddy1kmDistance  = 1
	buddy3kmDistance  = 3
	buddy5kmDistance  = 5
	buddy20kmDistance = 20

	buddy1kmCandy  = 25
	buddy3kmCandy  = 50
	buddy5kmCandy  = 75
	buddy20kmCandy = 100
)

// candyCostFromBuddy maps the Pokémon GO buddy-distance table:
// 1km → 25 candy, 3km → 50 candy, 5km → 75 candy, 20km → 100 candy.
// Any other value returns (0, false) — the caller surfaces this as
// "candy cost unavailable" via CandyCostAvailable=false.
func candyCostFromBuddy(kilometres int) (int, bool) {
	switch kilometres {
	case buddy1kmDistance:
		return buddy1kmCandy, true
	case buddy3kmDistance:
		return buddy3kmCandy, true
	case buddy5kmDistance:
		return buddy5kmCandy, true
	case buddy20kmDistance:
		return buddy20kmCandy, true
	default:
		return 0, false
	}
}

// buildSecondMoveCostNote composes a human-readable explanation of
// which fields in the response are load-bearing vs derived from
// missing data, and whether the shadow premium was applied. Parts
// are concatenated so partial-data shadow responses carry BOTH the
// shadow-premium note and the availability-caveat note.
func buildSecondMoveCostNote(stardust int, candyOK bool, opts CombatantOptions) string {
	// capacity upper bound: shadow note + purified note + one availability caveat.
	const maxNoteParts = 3

	parts := make([]string, 0, maxNoteParts)

	if opts.Shadow {
		parts = append(parts, "Shadow form: +20% (×1.2) on both stardust and candy.")
	}

	if opts.Purified {
		parts = append(parts, "Purified form: −10% (×0.9) on both stardust and candy.")
	}

	switch {
	case stardust == 0 && !candyOK:
		parts = append(parts,
			"Neither stardust nor candy cost is available for this species in the current gamemaster.")
	case stardust == 0:
		parts = append(parts,
			"Upstream does not publish a stardust cost for this species; candy cost is derived from buddy distance.")
	case !candyOK:
		parts = append(parts,
			"Upstream does not publish a buddy distance for this species; candy cost cannot be derived.")
	}

	return strings.Join(parts, " ")
}
