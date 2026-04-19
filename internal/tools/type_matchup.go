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
// human-readable calculation breakdown (e.g. `"grass vs water(1.60)
// × ground(1.60) = 2.56"`) so clients can explain WHY the multiplier
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

	err := validateTypeNames(atk, params.DefenderTypes)
	if err != nil {
		return nil, TypeMatchupResult{}, err
	}

	lowered := make([]string, 0, len(params.DefenderTypes))

	factors := make([]string, 0, len(params.DefenderTypes))
	for _, def := range params.DefenderTypes {
		if def == "" {
			continue
		}

		low := strings.ToLower(def)
		lowered = append(lowered, low)

		factor := pogopvp.TypeEffectiveness(atk, []string{low})
		factors = append(factors, fmt.Sprintf("%s(%.2f)", low, factor))
	}

	composite := pogopvp.TypeEffectiveness(atk, lowered)

	calculation := fmt.Sprintf("%s vs %s = %.2f",
		atk, strings.Join(factors, " × "), composite)

	if len(factors) == 0 {
		calculation = fmt.Sprintf("%s vs <no types> = %.2f", atk, composite)
	}

	return nil, TypeMatchupResult{
		AttackerType:  atk,
		DefenderTypes: lowered,
		Multiplier:    composite,
		Calculation:   calculation,
	}, nil
}

// knownPvPTypes is the closed set of 18 PvP types pvpoke uses. Used
// to reject garbage type names in params before pogopvp.TypeEffectiveness
// silently folds them to neutral (1.0).
//
//nolint:gochecknoglobals // fixed domain table mirroring the engine's type chart
var knownPvPTypes = map[string]struct{}{
	"normal":   {},
	"fire":     {},
	"water":    {},
	"electric": {},
	"grass":    {},
	"ice":      {},
	"fighting": {},
	"poison":   {},
	"ground":   {},
	"flying":   {},
	"psychic":  {},
	"bug":      {},
	"rock":     {},
	"ghost":    {},
	"dragon":   {},
	"dark":     {},
	"steel":    {},
	"fairy":    {},
}

// ErrUnknownType is returned by pvp_type_matchup when an attacker
// or defender type name is not one of pvpoke's 18 canonical types.
// The engine's TypeEffectiveness silently folds unknowns to neutral
// (1.0), which is a footgun for LLM callers that mistype — we
// reject explicitly so the caller can fix and retry.
var ErrUnknownType = errors.New("unknown type name")

// ErrTooManyDefenderTypes is returned when defender_types has more
// than 2 entries. Niantic's type system caps every Pokémon at 2
// types (primary, secondary); a 3+-entry list would produce a
// meaningless multiplier that an LLM consumer could not detect as
// nonsense from the multiplier alone.
var ErrTooManyDefenderTypes = errors.New("defender_types has more than 2 entries")

// maxDefenderTypes is the Niantic cap on a Pokémon's type count.
const maxDefenderTypes = 2

// validateTypeNames rejects any attacker or defender type outside
// the canonical 18. Empty defender-type entries are tolerated
// (they're trimmed by the composite calculation); nil / empty
// defender list is also fine (multiplier = 1.0, neutral).
func validateTypeNames(attacker string, defenders []string) error {
	_, ok := knownPvPTypes[attacker]
	if !ok {
		return fmt.Errorf("%w: attacker_type=%q not in pvpoke's 18 types",
			ErrUnknownType, attacker)
	}

	if len(defenders) > maxDefenderTypes {
		return fmt.Errorf("%w: got %d, max %d (Niantic caps Pokémon at 2 types)",
			ErrTooManyDefenderTypes, len(defenders), maxDefenderTypes)
	}

	for _, def := range defenders {
		if def == "" {
			continue
		}

		_, ok := knownPvPTypes[strings.ToLower(def)]
		if !ok {
			return fmt.Errorf("%w: defender_types contains %q, not in pvpoke's 18 types",
				ErrUnknownType, def)
		}
	}

	return nil
}
