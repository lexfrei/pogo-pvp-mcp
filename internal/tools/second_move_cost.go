package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SecondMoveCostParams is the JSON input for pvp_second_move_cost.
// Shadow species use the engine's "_shadow" suffix convention —
// matching pvp_rank / pvp_cp_limits / pvp_level_from_cp.
type SecondMoveCostParams struct {
	Species string `json:"species" jsonschema:"species id (shadow variants use e.g. \"medicham_shadow\")"`
}

// SecondMoveCostResult is the JSON output. Pokémon GO charges
// stardust AND candy to unlock a second charged move. pvpoke's
// gamemaster only carries the stardust number; the candy cost is
// derived from the species' buddy distance using Niantic's
// canonical table (1km → 25 candy, 3km → 50, 5km → 75, 20km → 100).
//
// CandyCostAvailable reports whether the candy derivation
// succeeded: false means the gamemaster does not publish a
// buddy distance for this species (no derivation possible), in
// which case CandyCost is zero. Callers must check the flag
// before acting on CandyCost — zero is not a valid Pokémon GO
// second-move candy cost.
type SecondMoveCostResult struct {
	Species               string `json:"species"`
	StardustCost          int    `json:"stardust_cost"`
	CandyCost             int    `json:"candy_cost"`
	BuddyDistanceKM       int    `json:"buddy_distance_km"`
	CandyCostAvailable    bool   `json:"candy_cost_available"`
	StardustCostAvailable bool   `json:"stardust_cost_available"`
	ShadowMultiplier      int    `json:"shadow_multiplier"`
	Note                  string `json:"note,omitempty"`
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
	"derived from the species' buddy distance (1km=25, 3km=50, 5km=75, 20km=100). Shadow species (id suffix " +
	"\"_shadow\") pay 3× both currencies. Zero fields with availability=false signal the upstream data is missing."

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

// shadowSpeciesSuffix matches the pvpoke / engine convention for
// shadow variants: "medicham_shadow", "mewtwo_shadow", etc.
const shadowSpeciesSuffix = "_shadow"

// shadowCostMultiplier is Niantic's confirmed 3× penalty on both
// stardust and candy when unlocking the second charged move on a
// shadow-form Pokémon.
const shadowCostMultiplier = 3

// handle looks up the species, derives the candy cost from its
// buddy distance, and applies the shadow multiplier if applicable.
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

	species, ok := snapshot.Pokemon[params.Species]
	if !ok {
		return nil, SecondMoveCostResult{}, fmt.Errorf("%w: %q", ErrUnknownSpecies, params.Species)
	}

	stardust := species.ThirdMoveCost
	candy, candyOK := candyCostFromBuddy(species.BuddyDistance)

	multiplier := 1
	if strings.HasSuffix(params.Species, shadowSpeciesSuffix) {
		multiplier = shadowCostMultiplier
		stardust *= shadowCostMultiplier

		if candyOK {
			candy *= shadowCostMultiplier
		}
	}

	return nil, SecondMoveCostResult{
		Species:               params.Species,
		StardustCost:          stardust,
		CandyCost:             candy,
		BuddyDistanceKM:       species.BuddyDistance,
		StardustCostAvailable: species.ThirdMoveCost > 0,
		CandyCostAvailable:    candyOK,
		ShadowMultiplier:      multiplier,
		Note:                  buildSecondMoveCostNote(species.ThirdMoveCost, candyOK, multiplier),
	}, nil
}

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
// missing data. LLMs consuming the tool use this to explain the
// result back to the user.
func buildSecondMoveCostNote(stardust int, candyOK bool, multiplier int) string {
	switch {
	case multiplier == shadowCostMultiplier && stardust > 0 && candyOK:
		return "Shadow form: 3× both stardust and candy. Purified forms get a 20% discount on both currencies " +
			"(not modelled here — re-query the purified species id)."
	case stardust == 0 && !candyOK:
		return "Neither stardust nor candy cost is available for this species in the current gamemaster."
	case stardust == 0:
		return "Upstream does not publish a stardust cost for this species. Candy cost is derived from buddy " +
			"distance."
	case !candyOK:
		return "Upstream does not publish a buddy distance for this species. Stardust cost comes from the " +
			"gamemaster; candy cost cannot be derived."
	default:
		return ""
	}
}
