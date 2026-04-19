package tools

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrInvalidLevel is returned when a level is below 1.0 or not on
// the canonical 0.5 grid.
var ErrInvalidLevel = errors.New("invalid level")

// ErrLevelRangeEmpty is returned when to_level is at or below
// from_level — no powerup steps to charge for.
var ErrLevelRangeEmpty = errors.New("to_level must be strictly greater than from_level")

// PowerupCostParams is the JSON input for pvp_powerup_cost. Options
// carries the per-Pokémon modifier flags (Phase X): Shadow ×1.2,
// Purified ×0.9, Lucky ×0.5 on stardust (powerup-specific). All
// three stack multiplicatively (Lucky+Purified = 0.45×, etc.) —
// scaleCost's integer arithmetic keeps every canonical tier exact
// because the pre-XL stardust values (200..10000) and the XL-era
// values (10000..15000) are all multiples of 100.
//
// Candy cost is still NOT emitted: public sources disagree on the
// per-half-step candy table (Bulbapedia's published bucket totals
// fail their own arithmetic check — 19×1+20×2+12×3+26×4 = 199, not
// the 304 they claim). The tool refuses to guess and directs
// callers to an authoritative external source. Same applies to XL
// candy in the L40-L50 range.
type PowerupCostParams struct {
	FromLevel float64          `json:"from_level" jsonschema:"starting level on the 0.5 grid (min 1.0)"`
	ToLevel   float64          `json:"to_level" jsonschema:"target level on the 0.5 grid (max 50.0)"`
	Options   CombatantOptions `json:"options,omitzero" jsonschema:"shadow / lucky / purified flags; stack multiplicatively"`
}

// PowerupCostResult is the JSON output for pvp_powerup_cost. All
// values are already-multiplied by CostMultiplier so callers do not
// need to re-apply the flag discount/premium. BaselineStardustCost
// echoes the pre-multiplier number for transparency.
//
// Candy cost is intentionally NOT emitted — see PowerupCostParams
// godoc for the reasoning. Note surfaces the decision to every
// response so LLM clients reading the tool output are immediately
// told why the field is absent.
type PowerupCostResult struct {
	FromLevel            float64 `json:"from_level"`
	ToLevel              float64 `json:"to_level"`
	Steps                int     `json:"steps"`
	StardustCost         int     `json:"stardust_cost"`
	BaselineStardustCost int     `json:"baseline_stardust_cost"`
	CostMultiplier       float64 `json:"cost_multiplier"`
	StardustMultiplier   float64 `json:"stardust_multiplier"`
	CrossesXLBoundary    bool    `json:"crosses_xl_boundary,omitempty"`
	XLStepsIncluded      int     `json:"xl_steps_included,omitempty"`
	Note                 string  `json:"note"`
}

// PowerupCostTool is a zero-dependency lookup over the canonical
// Pokémon GO powerup stardust table (L1-L50 half-level steps).
type PowerupCostTool struct{}

// NewPowerupCostTool constructs the tool.
func NewPowerupCostTool() *PowerupCostTool {
	return &PowerupCostTool{}
}

const powerupCostToolDescription = "Compute the stardust cost of powering up a Pokémon from one level to " +
	"another in 0.5-level increments. Covers L1-L50 — both the pre-XL era (L1→L40, 270,000 total) and the " +
	"XL-candy era (L40→L50, +250,000 total). Candy cost is intentionally NOT returned: public sources disagree " +
	"on the per-half-step candy table and we refuse to hand out guessed numbers. Options.Shadow (×1.2), " +
	"Options.Purified (×0.9), Options.Lucky (×0.5 stardust-only) scale the result; flags stack multiplicatively."

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

// minPowerupLevel and maxPowerupLevel bound the full L1-L50 table.
const (
	minPowerupLevel = 1.0
	maxPowerupLevel = 50.0
)

// powerupStardustTable is the canonical Pokémon GO stardust cost
// per 0.5-level step from L1.0 to L50.0. Indexed by (doubled-level
// - 2) — index 0 is the L1.0→L1.5 step, index 97 is the L49.5→L50.0
// step. Values sourced from Niantic's in-game display, cross-
// checked against Bulbapedia's Power_up page; stardust totals are
// self-consistent (L1→L40 = 270,000, L40→L50 = +250,000) unlike
// the candy table, which is why stardust ships and candy does not.
//
//nolint:gochecknoglobals // fixed domain lookup table
var powerupStardustTable = buildPowerupStardustTable()

// buildPowerupStardustTable constructs the 98-entry table from the
// 20 pre-XL buckets (4 half-level steps each, except L39-L40 at 2)
// and the 6 XL-era buckets (spanning L40.0-L49.5, 20 half-level
// steps in total).
func buildPowerupStardustTable() []int {
	preXL := []struct {
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

	// XL era — Bulbapedia Power_up page; stardust totals sum to
	// 250,000 across 20 half-level steps. The first bucket is 2
	// steps (L40.0, L40.5) at the same 10000/step as the final
	// pre-XL bucket; subsequent buckets ramp up 1000/tier every two
	// full levels. Last bucket is 2 steps (L49.0, L49.5) at 15000.
	xlEra := []struct {
		Steps    int
		Stardust int
	}{
		{2, 10000}, // L40.0-L40.5 (XL era continues the 10k tier)
		{4, 11000}, // L41.0-L42.5
		{4, 12000}, // L43.0-L44.5
		{4, 13000}, // L45.0-L46.5
		{4, 14000}, // L47.0-L48.5
		{2, 15000}, // L49.0-L49.5
	}

	out := make([]int, 0, totalPowerupSteps)

	for _, b := range preXL {
		for range b.Steps {
			out = append(out, b.Stardust)
		}
	}

	for _, b := range xlEra {
		for range b.Steps {
			out = append(out, b.Stardust)
		}
	}

	return out
}

// preXLTotalSteps is the number of 0.5-level steps from L1.0 to
// L40.0 inclusive: 19 buckets of 4 steps + 1 bucket of 2 steps = 78.
// xlEraSteps is the number of 0.5-level steps from L40.0 to L50.0:
// 2+4+4+4+4+2 = 20. totalPowerupSteps = 98. The stardust totals
// per era (270,000 pre-XL, 250,000 XL) are not declared as consts
// — the tests recompute them from the table so a table edit that
// silently changes the total is caught instead of rubber-stamped.
const (
	preXLTotalSteps   = 78
	xlEraSteps        = 20
	totalPowerupSteps = preXLTotalSteps + xlEraSteps
)

// powerupStardustMultiplierFor applies the Options flags to the
// stardust baseline and returns the final multiplier. Stardust
// takes all three flags (Shadow ×1.2, Purified ×0.9, Lucky ×0.5);
// each flag is independent-zero-one so we can test them bitwise
// and compose the multiplier lazily.
func powerupStardustMultiplierFor(opts CombatantOptions) float64 {
	m := 1.0

	if opts.Shadow {
		m *= shadowCostMultiplier
	}

	if opts.Purified {
		m *= purifiedCostMultiplier
	}

	if opts.Lucky {
		m *= luckyStardustMultiplier
	}

	return m
}

// luckyStardustMultiplier is the 50% stardust discount Niantic
// applies to lucky Pokémon during power-up. Unlike shadow / purified
// which affect both currencies, lucky is stardust-only and does NOT
// reduce candy cost — documented on Bulbapedia's Lucky_Pokémon page.
const luckyStardustMultiplier = 0.5

// scaleStardust applies a float multiplier to an integer baseline
// using integer arithmetic where it is exact (Shadow ×1.2 via
// ×12/÷10, Purified ×0.9 via ×9/÷10, Lucky ×0.5 via ÷2). Stacked
// combinations fall back to float multiplication with round-half-
// to-even rounding — all canonical tiers are still exact multiples
// of 100 after every stack since the pre-XL and XL stardust values
// are all ×100 integers and the multipliers are each exact
// tenths / halves.
func scaleStardust(baseline int, multiplier float64) int {
	switch multiplier {
	case 1.0:
		return baseline
	case shadowCostMultiplier:
		return baseline * 12 / 10
	case purifiedCostMultiplier:
		return baseline * 9 / 10
	case luckyStardustMultiplier:
		return baseline / 2
	}

	return int(math.Round(float64(baseline) * multiplier))
}

// countXLSteps returns how many [fromIdx, toIdx) half-steps begin
// at or above L40.0. The breakpoint index is preXLTotalSteps
// (index 78 = the L40.0→L40.5 step). Reported on the response so
// callers doing XL-candy budget planning see the XL-era overlap
// even though candy itself is not emitted.
func countXLSteps(fromIdx, toIdx int) int {
	if toIdx <= preXLTotalSteps {
		return 0
	}

	if fromIdx >= preXLTotalSteps {
		return toIdx - fromIdx
	}

	return toIdx - preXLTotalSteps
}

// handle validates the level range, sums stardust across the half-
// level steps, and applies the Options multiplier. Candy is
// deliberately not computed (see PowerupCostResult godoc).
func (tool *PowerupCostTool) handle(
	_ context.Context,
	_ *mcp.CallToolRequest,
	params PowerupCostParams,
) (*mcp.CallToolResult, PowerupCostResult, error) {
	fromIdx, toIdx, err := validatePowerupRange(params.FromLevel, params.ToLevel)
	if err != nil {
		return nil, PowerupCostResult{}, err
	}

	var baselineStardust int

	for i := fromIdx; i < toIdx; i++ {
		baselineStardust += powerupStardustTable[i]
	}

	stardustMult := powerupStardustMultiplierFor(params.Options)
	overallMult := costMultiplierFor(params.Options)
	scaledStardust := scaleStardust(baselineStardust, stardustMult)
	xlSteps := countXLSteps(fromIdx, toIdx)

	return nil, PowerupCostResult{
		FromLevel:            params.FromLevel,
		ToLevel:              params.ToLevel,
		Steps:                toIdx - fromIdx,
		StardustCost:         scaledStardust,
		BaselineStardustCost: baselineStardust,
		CostMultiplier:       overallMult,
		StardustMultiplier:   stardustMult,
		CrossesXLBoundary:    xlSteps > 0,
		XLStepsIncluded:      xlSteps,
		Note: "Candy cost is NOT returned: public sources disagree on the per-half-step candy table " +
			"(Bulbapedia's own bucket totals fail arithmetic). Consult an authoritative external source " +
			"if you need the candy number. Lucky affects stardust only (Niantic's 50% discount is " +
			"powerup-specific).",
	}, nil
}

// validatePowerupRange converts the user-facing (fromLevel, toLevel)
// pair into step indices into powerupStardustTable. Each index
// represents the 0.5-level step STARTING at that level, so the
// iteration runs [fromIdx, toIdx) — i.e. fromIdx=0 toIdx=98 sums
// the full L1→L50 climb.
//
// Level values must:
//   - Lie on the 0.5 grid (doubled level is a positive integer).
//   - Stay inside [1.0, 50.0].
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
			ErrInvalidLevel, fromLevel, maxPowerupLevel)
	}

	if toDoubled > int(maxPowerupLevel*2) {
		return 0, 0, fmt.Errorf("%w: to_level %.1f > %.1f",
			ErrInvalidLevel, toLevel, maxPowerupLevel)
	}

	if toDoubled <= fromDoubled {
		return 0, 0, fmt.Errorf("%w: from_level=%.1f, to_level=%.1f",
			ErrLevelRangeEmpty, fromLevel, toLevel)
	}

	return fromDoubled - int(minPowerupLevel*2), toDoubled - int(minPowerupLevel*2), nil
}

// doubleLevelOnHalfGrid converts a level to its doubled integer
// representation (L1→2, L1.5→3, L50→100) and validates that the
// input lies on the 0.5 grid at or above L1. The upper bound is
// checked in validatePowerupRange.
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
