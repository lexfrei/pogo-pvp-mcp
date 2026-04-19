package tools

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrInvalidLevel is returned when a level is below 1.0 or not on
// the canonical 0.5 grid. Levels above 40.0 are classified
// separately as ErrXLRangeNotSupported (different failure mode,
// more actionable error for LLM callers).
var ErrInvalidLevel = errors.New("invalid level")

// ErrXLRangeNotSupported signals the tool was asked to price levels
// past 40 (the XL-candy era). The pre-XL table is well-documented and
// stable; the XL-era stardust/candy/XL-candy costs have shifted with
// post-2020 Niantic adjustments and are not modelled here to avoid
// handing callers outdated numbers.
var ErrXLRangeNotSupported = errors.New("powerup cost above level 40 (XL candy era) is not modelled")

// ErrLevelRangeEmpty is returned when to_level is at or below
// from_level — no powerup steps to charge for.
var ErrLevelRangeEmpty = errors.New("to_level must be strictly greater than from_level")

// PowerupCostParams is the JSON input for pvp_powerup_cost.
type PowerupCostParams struct {
	FromLevel float64 `json:"from_level" jsonschema:"starting level on the 0.5 grid (min 1.0)"`
	ToLevel   float64 `json:"to_level" jsonschema:"target level on the 0.5 grid (max 40.0; XL-candy era above 40 is not modelled)"`
}

// PowerupCostResult is the JSON output for pvp_powerup_cost.
// Stardust is the total dust needed to power up from_level →
// to_level in 0.5-level steps. Steps is the number of 0.5-level
// upgrades walked (e.g. L1→L40 = 78).
//
// Candy cost is intentionally NOT emitted. The stardust table is
// well-documented and stable since the XL-candy system launched;
// the candy-per-step table has staggered boundaries that
// disagree across publicly-cited sources, so we refuse to guess.
// Callers that need the candy cost should consult Bulbapedia /
// GamePress / pvpoke directly.
type PowerupCostResult struct {
	FromLevel    float64 `json:"from_level"`
	ToLevel      float64 `json:"to_level"`
	Steps        int     `json:"steps"`
	StardustCost int     `json:"stardust_cost"`
	Note         string  `json:"note"`
}

// PowerupCostTool is a zero-dependency lookup over the canonical
// Pokémon GO powerup-cost table (L1-L40, pre-XL era).
type PowerupCostTool struct{}

// NewPowerupCostTool constructs the tool.
func NewPowerupCostTool() *PowerupCostTool {
	return &PowerupCostTool{}
}

const powerupCostToolDescription = "Compute the stardust cost of powering up a Pokémon from one level to another " +
	"in 0.5-level increments. Pre-XL range L1-L40 only (well-documented table, stable since the XL-candy system " +
	"launched). Candy cost is intentionally NOT returned — the per-step candy table has staggered boundaries that " +
	"disagree across publicly-cited sources and we refuse to hand out guessed numbers. Level > 40 (XL-candy era) " +
	"is rejected with ErrXLRangeNotSupported."

// Tool returns the MCP tool registration.
func (*PowerupCostTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_powerup_cost",
		Description: powerupCostToolDescription,
	}
}

// Handler returns the MCP-typed handler function.
func (tool *PowerupCostTool) Handler() mcp.ToolHandlerFor[PowerupCostParams, PowerupCostResult] {
	return tool.handle
}

// minPowerupLevel and maxPowerupLevel bound the pre-XL era. The
// canonical table covers 78 half-level steps from L1.0 to L40.0;
// anything beyond 40 is XL-candy era and rejected.
const (
	minPowerupLevel = 1.0
	maxPowerupLevel = 40.0
)

// powerupStardustTable is the canonical Pokémon GO stardust cost
// per 0.5-level step from L1.0 to L40.0. Indexed by
// (doubled-level - 2) — index 0 is the L1.0→L1.5 step, index 77
// is the L39.5→L40.0 step. Values sourced from Niantic's in-game
// display; stable since the XL-candy system launched.
//
//nolint:gochecknoglobals // fixed domain lookup table
var powerupStardustTable = buildPowerupStardustTable()

// buildPowerupStardustTable constructs the 78-entry table from the
// 20 level buckets (bucket k covers 4 half-level steps each, except
// the last bucket L39-L40 which is only 2 half-level steps).
func buildPowerupStardustTable() []int {
	buckets := []struct {
		Steps    int
		Stardust int
	}{
		{4, 200},   // L1.0-L2.5
		{4, 400},   // L3.0-L4.5
		{4, 600},   // L5.0-L6.5
		{4, 800},   // L7.0-L8.5
		{4, 1000},  // L9.0-L10.5
		{4, 1300},  // L11.0-L12.5
		{4, 1600},  // L13.0-L14.5
		{4, 1900},  // L15.0-L16.5
		{4, 2200},  // L17.0-L18.5
		{4, 2500},  // L19.0-L20.5
		{4, 3000},  // L21.0-L22.5
		{4, 3500},  // L23.0-L24.5
		{4, 4000},  // L25.0-L26.5
		{4, 4500},  // L27.0-L28.5
		{4, 5000},  // L29.0-L30.5
		{4, 6000},  // L31.0-L32.5
		{4, 7000},  // L33.0-L34.5
		{4, 8000},  // L35.0-L36.5
		{4, 9000},  // L37.0-L38.5
		{2, 10000}, // L39.0-L40.0 — only 2 half-level steps
	}

	out := make([]int, 0, preXLTotalSteps)

	for _, b := range buckets {
		for range b.Steps {
			out = append(out, b.Stardust)
		}
	}

	return out
}

// preXLTotalSteps is the number of 0.5-level steps from L1.0 to
// L40.0 inclusive: 19 buckets of 4 steps + 1 bucket of 2 steps = 78.
const preXLTotalSteps = 78

// handle validates the level range, then sums stardust across the
// half-level steps. Candy is deliberately not computed (see
// PowerupCostResult godoc).
func (tool *PowerupCostTool) handle(
	_ context.Context,
	_ *mcp.CallToolRequest,
	params PowerupCostParams,
) (*mcp.CallToolResult, PowerupCostResult, error) {
	fromIdx, toIdx, err := validatePowerupRange(params.FromLevel, params.ToLevel)
	if err != nil {
		return nil, PowerupCostResult{}, err
	}

	var stardust int

	for i := fromIdx; i < toIdx; i++ {
		stardust += powerupStardustTable[i]
	}

	return nil, PowerupCostResult{
		FromLevel:    params.FromLevel,
		ToLevel:      params.ToLevel,
		Steps:        toIdx - fromIdx,
		StardustCost: stardust,
		Note: "Candy cost is not returned: public sources disagree on the per-half-step candy table " +
			"above L20. Consult Bulbapedia / GamePress / pvpoke if you need the candy number.",
	}, nil
}

// validatePowerupRange converts the user-facing (fromLevel, toLevel)
// pair into inclusive step indices into powerupStepTable. Each index
// represents the 0.5-level step STARTING at that level, so the
// iteration runs [fromIdx, toIdx) — i.e. fromIdx=0 toIdx=78 sums the
// full L1→L40 climb.
//
// Level values must:
//   - Lie on the 0.5 grid (doubled level is a positive integer).
//   - Stay inside [1.0, 40.0].
//   - Satisfy fromLevel < toLevel.
//
//nolint:gocritic // unnamedResult: returns (fromIdx, toIdx, err) documented above
func validatePowerupRange(fromLevel, toLevel float64) (int, int, error) {
	fromDoubled, err := doubleLevelOnHalfGrid(fromLevel, "from_level")
	if err != nil {
		return 0, 0, err
	}

	toDoubled, err := doubleLevelOnHalfGrid(toLevel, "to_level")
	if err != nil {
		return 0, 0, err
	}

	if fromDoubled > int(maxPowerupLevel*2) {
		return 0, 0, fmt.Errorf("%w: from_level %.1f > %.1f",
			ErrXLRangeNotSupported, fromLevel, maxPowerupLevel)
	}

	if toDoubled > int(maxPowerupLevel*2) {
		return 0, 0, fmt.Errorf("%w: to_level %.1f > %.1f",
			ErrXLRangeNotSupported, toLevel, maxPowerupLevel)
	}

	if toDoubled <= fromDoubled {
		return 0, 0, fmt.Errorf("%w: from_level=%.1f, to_level=%.1f",
			ErrLevelRangeEmpty, fromLevel, toLevel)
	}

	return fromDoubled - int(minPowerupLevel*2), toDoubled - int(minPowerupLevel*2), nil
}

// doubleLevelOnHalfGrid converts a level to its doubled integer
// representation (L1→2, L1.5→3, L40→80) and validates that the
// input lies on the 0.5 grid at or above L1. The upper bound is
// NOT checked here: validatePowerupRange classifies >40 as
// ErrXLRangeNotSupported separately (different failure mode, more
// actionable error).
func doubleLevelOnHalfGrid(level float64, label string) (int, error) {
	const halfGridTolerance = 1e-9 // float64 quantisation slack on the 0.5 grid

	if math.IsNaN(level) || math.IsInf(level, 0) {
		return 0, fmt.Errorf("%w: %s=%v is not a finite number",
			ErrInvalidLevel, label, level)
	}

	doubled := math.Round(level * 2)

	if math.Abs(level*2-doubled) > halfGridTolerance {
		return 0, fmt.Errorf("%w: %s=%.3f is not on the 0.5 grid",
			ErrInvalidLevel, label, level)
	}

	doubledInt := int(doubled)
	if doubledInt < int(minPowerupLevel*2) {
		return 0, fmt.Errorf("%w: %s=%.1f below minimum level %.1f",
			ErrInvalidLevel, label, level, minPowerupLevel)
	}

	return doubledInt, nil
}
