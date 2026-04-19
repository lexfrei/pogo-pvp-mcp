package tools

import (
	"context"
	"fmt"

	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SecondMoveCostParams is the JSON input for pvp_second_move_cost.
type SecondMoveCostParams struct {
	Species string `json:"species" jsonschema:"species id in the pvpoke gamemaster (shadow variants use e.g. \"medicham_shadow\")"`
}

// SecondMoveCostResult is the JSON output. StardustCost and
// CandyCost carry the same numeric value — pvpoke's gamemaster
// stores one `thirdMoveCost` field and Pokémon GO charges the
// same number of each currency. Zero values signal the species
// has no published cost (usually an un-released form or a Pokémon
// whose second move is never unlockable, e.g. ditto).
type SecondMoveCostResult struct {
	Species      string `json:"species"`
	StardustCost int    `json:"stardust_cost"`
	CandyCost    int    `json:"candy_cost"`
	Available    bool   `json:"available"`
	Note         string `json:"note,omitempty"`
}

// SecondMoveCostTool wraps the gamemaster manager.
type SecondMoveCostTool struct {
	gm *gamemaster.Manager
}

// NewSecondMoveCostTool constructs the tool.
func NewSecondMoveCostTool(gm *gamemaster.Manager) *SecondMoveCostTool {
	return &SecondMoveCostTool{gm: gm}
}

const secondMoveCostToolDescription = "Given a species id, return the stardust + candy cost to unlock a second " +
	"charged move slot. Values are read from the gamemaster's thirdMoveCost field (pvpoke uses the same number " +
	"for both currencies). A zero value signals the species has no published cost — usually an un-released form."

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

// handle looks up the species and returns its thirdMoveCost; a zero
// value is reported as Available=false with an explanatory Note.
func (tool *SecondMoveCostTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params SecondMoveCostParams,
) (*mcp.CallToolResult, SecondMoveCostResult, error) {
	err := ctx.Err()
	if err != nil {
		return nil, SecondMoveCostResult{}, fmt.Errorf("second_move_cost cancelled: %w", err)
	}

	if params.Species == "" {
		return nil, SecondMoveCostResult{}, fmt.Errorf("%w: species required", ErrUnknownSpecies)
	}

	snapshot := tool.gm.Current()
	if snapshot == nil {
		return nil, SecondMoveCostResult{}, ErrGamemasterNotLoaded
	}

	species, ok := snapshot.Pokemon[params.Species]
	if !ok {
		return nil, SecondMoveCostResult{}, fmt.Errorf("%w: %q", ErrUnknownSpecies, params.Species)
	}

	cost := species.ThirdMoveCost
	if cost == 0 {
		return nil, SecondMoveCostResult{
			Species:      params.Species,
			StardustCost: 0,
			CandyCost:    0,
			Available:    false,
			Note:         "No second-move cost is published for this species in the current gamemaster.",
		}, nil
	}

	return nil, SecondMoveCostResult{
		Species:      params.Species,
		StardustCost: cost,
		CandyCost:    cost,
		Available:    true,
	}, nil
}
