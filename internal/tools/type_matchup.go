package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrMissingAttackerType is returned when TypeMatchupParams.AttackerType
// is empty — the move's attacking type is a required input.
var ErrMissingAttackerType = errors.New("attacker_type is required")

// TypeMatchupParams is the JSON input contract for pvp_type_matchup.
// DefenderTypes mirrors pvpoke's list order (primary, secondary); a
// single-type defender passes a one-element list. An empty list is
// accepted and yields 1.0 per pogopvp.TypeEffectiveness semantics.
type TypeMatchupParams struct {
	AttackerType  string   `json:"attacker_type" jsonschema:"the attacking move's type (e.g. \"grass\")"`
	DefenderTypes []string `json:"defender_types" jsonschema:"defender's type list (1 or 2 entries)"`
}

// TypeMatchupResult reports the composite multiplier plus a
// human-readable calculation breakdown (e.g. `"grass vs water(1.6)
// × ground(1.6) = 2.56"`) so clients can explain WHY the multiplier
// landed where it did without re-running the math.
type TypeMatchupResult struct {
	AttackerType  string   `json:"attacker_type"`
	DefenderTypes []string `json:"defender_types"`
	Multiplier    float64  `json:"multiplier"`
	Calculation   string   `json:"calculation"`
}

// TypeMatchupTool is a pure lookup over the engine type chart.
type TypeMatchupTool struct{}

// NewTypeMatchupTool constructs the tool. No dependencies — the
// type chart is baked into pogopvp.TypeEffectiveness.
func NewTypeMatchupTool() *TypeMatchupTool {
	return &TypeMatchupTool{}
}

const typeMatchupToolDescription = "Compute the damage multiplier a move of the given type deals to a " +
	"defender with the given type list. Returns the composite number plus a human-readable breakdown."

// Tool returns the MCP registration.
func (tool *TypeMatchupTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_type_matchup",
		Description: typeMatchupToolDescription,
	}
}

// Handler returns the MCP-typed handler.
func (tool *TypeMatchupTool) Handler() mcp.ToolHandlerFor[TypeMatchupParams, TypeMatchupResult] {
	return tool.handle
}

// handle validates inputs and assembles the breakdown string by
// calling TypeEffectiveness once per defender type for the factors,
// then once more over the full list for the composite — that second
// call is the authoritative multiplier (avoids float-rounding drift
// from multiplying per-type factors ourselves).
func (tool *TypeMatchupTool) handle(
	_ context.Context,
	_ *mcp.CallToolRequest,
	params TypeMatchupParams,
) (*mcp.CallToolResult, TypeMatchupResult, error) {
	if params.AttackerType == "" {
		return nil, TypeMatchupResult{}, ErrMissingAttackerType
	}

	atk := strings.ToLower(params.AttackerType)

	factors := make([]string, 0, len(params.DefenderTypes))
	for _, def := range params.DefenderTypes {
		if def == "" {
			continue
		}

		factor := pogopvp.TypeEffectiveness(atk, []string{def})
		factors = append(factors, fmt.Sprintf("%s(%.2f)", strings.ToLower(def), factor))
	}

	composite := pogopvp.TypeEffectiveness(atk, params.DefenderTypes)

	calculation := fmt.Sprintf("%s vs %s = %.2f",
		atk, strings.Join(factors, " × "), composite)

	if len(factors) == 0 {
		calculation = fmt.Sprintf("%s vs <no types> = %.2f", atk, composite)
	}

	return nil, TypeMatchupResult{
		AttackerType:  atk,
		DefenderTypes: params.DefenderTypes,
		Multiplier:    composite,
		Calculation:   calculation,
	}, nil
}
