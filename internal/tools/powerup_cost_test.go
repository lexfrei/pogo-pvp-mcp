package tools_test

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

// TestPowerupCost_L1ToL40 pins the full pre-XL stardust climb: 78
// half-level steps from L1 to L40 cost exactly 270,000 stardust,
// a value documented by every Pokémon GO power-up source and
// stable since the XL-candy system introduced the cap.
//
//	stardust = 4*(200+400+600+800+1000+1300+1600+1900+2200+2500+
//	            3000+3500+4000+4500+5000+6000+7000+8000+9000)
//	         + 2*10000
//	         = 270,000
//
// Candy cost is deliberately not checked here because the tool
// no longer returns it — per-half-step candy boundaries disagree
// across public sources.
func TestPowerupCost_L1ToL40(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 1.0,
		ToLevel:   40.0,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Steps != 78 {
		t.Errorf("Steps = %d, want 78", result.Steps)
	}

	if result.StardustCost != 270000 {
		t.Errorf("StardustCost = %d, want 270000", result.StardustCost)
	}

	if result.Note == "" {
		t.Errorf("Note empty; candy-omission disclaimer required on every response")
	}
}

// TestPowerupCost_SingleStep pins one step: L1.0 → L1.5 costs
// 200 stardust.
func TestPowerupCost_SingleStep(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 1.0,
		ToLevel:   1.5,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Steps != 1 {
		t.Errorf("Steps = %d, want 1", result.Steps)
	}

	if result.StardustCost != 200 {
		t.Errorf("StardustCost = %d, want 200", result.StardustCost)
	}
}

// TestPowerupCost_BucketBoundaries exercises the stardust bucket
// edges: last step of each bucket must share the bucket's rate,
// next step picks up the following bucket's rate.
func TestPowerupCost_BucketBoundaries(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	cases := []struct {
		name         string
		fromLevel    float64
		toLevel      float64
		wantStardust int
	}{
		{"L29.0-L29.5 (first step of 5000 bucket)", 29.0, 29.5, 5000},
		{"L30.5-L31.0 (last step of 5000 bucket)", 30.5, 31.0, 5000},
		{"L31.0-L31.5 (first step of 6000 bucket)", 31.0, 31.5, 6000},
		{"L39.5-L40.0 (final pre-XL step)", 39.5, 40.0, 10000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
				FromLevel: tc.fromLevel,
				ToLevel:   tc.toLevel,
			})
			if err != nil {
				t.Fatalf("handler: %v", err)
			}

			if result.StardustCost != tc.wantStardust {
				t.Errorf("StardustCost = %d, want %d", result.StardustCost, tc.wantStardust)
			}
		})
	}
}

// TestPowerupCost_XLEraSingleStep pins the first XL-era step
// (L40.0→L40.5) at the canonical 10,000 stardust. The step uses
// the same tier as the final pre-XL bucket; Bulbapedia's Power_up
// page confirms the 10k/step tier carries forward for two half-
// steps into the XL era before ramping to 11k/step at L41.
func TestPowerupCost_XLEraSingleStep(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 40.0,
		ToLevel:   40.5,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	const wantStardust = 10000

	if result.StardustCost != wantStardust {
		t.Errorf("StardustCost = %d, want %d", result.StardustCost, wantStardust)
	}

	if result.XLStepsIncluded != 1 {
		t.Errorf("XLStepsIncluded = %d, want 1", result.XLStepsIncluded)
	}

	if !result.CrossesXLBoundary {
		t.Errorf("CrossesXLBoundary = false, want true (step L40.0 is in XL era)")
	}
}

// TestPowerupCost_XLRangeFromLevelAccepted sums stardust from an
// XL-era start (L41.0) to cover the 11,000/step tier.
func TestPowerupCost_XLRangeFromLevelAccepted(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 41.0,
		ToLevel:   41.5,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	const wantStardust = 11000

	if result.StardustCost != wantStardust {
		t.Errorf("StardustCost = %d, want %d (L41.0→L41.5 is first 11k tier step)",
			result.StardustCost, wantStardust)
	}
}

// TestPowerupCost_OffGridLevel rejects levels that don't land on
// the 0.5 grid.
func TestPowerupCost_OffGridLevel(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 1.3,
		ToLevel:   2.0,
	})
	if !errors.Is(err, tools.ErrInvalidLevel) {
		t.Errorf("error = %v, want wrapping ErrInvalidLevel (1.3 is off-grid)", err)
	}
}

// TestPowerupCost_BelowMinLevel rejects levels below L1.
func TestPowerupCost_BelowMinLevel(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 0.5,
		ToLevel:   2.0,
	})
	if !errors.Is(err, tools.ErrInvalidLevel) {
		t.Errorf("error = %v, want wrapping ErrInvalidLevel (0.5 is below L1)", err)
	}
}

// TestPowerupCost_EmptyRange rejects to_level <= from_level.
func TestPowerupCost_EmptyRange(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 20.0,
		ToLevel:   20.0,
	})
	if !errors.Is(err, tools.ErrLevelRangeEmpty) {
		t.Errorf("error = %v, want wrapping ErrLevelRangeEmpty", err)
	}
}

// TestPowerupCost_AdjacentRangesSum validates that splitting a
// climb into two sub-ranges and summing yields the same total as
// one direct query. A basic consistency check for the indexing.
func TestPowerupCost_AdjacentRangesSum(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, whole, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 10.0,
		ToLevel:   30.0,
	})
	if err != nil {
		t.Fatalf("whole: %v", err)
	}

	_, left, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 10.0,
		ToLevel:   20.0,
	})
	if err != nil {
		t.Fatalf("left: %v", err)
	}

	_, right, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 20.0,
		ToLevel:   30.0,
	})
	if err != nil {
		t.Fatalf("right: %v", err)
	}

	if whole.StardustCost != left.StardustCost+right.StardustCost {
		t.Errorf("stardust sum mismatch: whole=%d, left+right=%d",
			whole.StardustCost, left.StardustCost+right.StardustCost)
	}
}

// TestPowerupCost_NoteExplainsCandyOmission locks the candy-omission
// disclaimer into every response. Without this, a caller might
// miss that the tool omits candy by design.
func TestPowerupCost_NoteExplainsCandyOmission(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 1.0,
		ToLevel:   40.0,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if !strings.Contains(strings.ToLower(result.Note), "candy") {
		t.Errorf("Note missing candy disclaimer; got %q", result.Note)
	}
}

// TestPowerupCost_JSONShape locks the wire shape of the result. A
// regression where someone re-adds a CandyCost field or bogus
// availability flags will break this test — the README and
// CLAUDE.md both claim the shape, and round-1 review caught
// copy-paste drift that advertised flags the code never emitted.
func TestPowerupCost_JSONShape(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 1.0,
		ToLevel:   2.0,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	decoded := map[string]any{}

	err = json.Unmarshal(payload, &decoded)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	wantKeys := []string{
		"from_level", "to_level", "steps",
		"stardust_cost", "baseline_stardust_cost",
		"cost_multiplier", "stardust_multiplier",
		"note",
	}

	gotKeys := make([]string, 0, len(decoded))
	for k := range decoded {
		gotKeys = append(gotKeys, k)
	}

	slices.Sort(wantKeys)
	slices.Sort(gotKeys)

	if !slices.Equal(gotKeys, wantKeys) {
		t.Errorf("JSON keys = %v, want %v", gotKeys, wantKeys)
	}
}

// TestPowerupCost_NaNRejected pins the NaN / Inf defensive guard.
// Without it, a caller passing a non-finite float would pass the
// 0.5-grid tolerance check vacuously (NaN comparisons are false)
// and drift into undefined int-conversion territory.
func TestPowerupCost_NaNRejected(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	cases := []struct {
		name  string
		level float64
	}{
		{"NaN", math.NaN()},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := handler(t.Context(), nil, tools.PowerupCostParams{
				FromLevel: tc.level,
				ToLevel:   40.0,
			})
			if !errors.Is(err, tools.ErrInvalidLevel) {
				t.Errorf("error = %v, want wrapping ErrInvalidLevel for %s", err, tc.name)
			}
		})
	}
}

// TestPowerupCost_DescriptionSanity locks the disclaimers into the
// MCP tool description so LLM clients reading the schema cannot
// miss them.
func TestPowerupCost_DescriptionSanity(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	desc := tool.Tool().Description

	if desc == "" {
		t.Fatal("description is empty")
	}

	descLower := strings.ToLower(desc)

	wantFragments := []string{"l1-l50", "xl", "candy", "shadow", "purified", "lucky"}
	for _, frag := range wantFragments {
		if !strings.Contains(descLower, frag) {
			t.Errorf("description missing fragment %q; got %q", frag, desc)
		}
	}
}

// TestPowerupCost_XLEraFullClimbTotal pins Niantic's published
// L40→L50 stardust total at 250,000 (20 half-level steps summing
// to the Bulbapedia Power_up table's XL-era figure). A future
// table edit that miscalibrates even one bucket flips this.
func TestPowerupCost_XLEraFullClimbTotal(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 40.0,
		ToLevel:   50.0,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	const wantStardust = 250000

	if result.StardustCost != wantStardust {
		t.Errorf("StardustCost L40→L50 = %d, want %d", result.StardustCost, wantStardust)
	}

	if result.Steps != 20 {
		t.Errorf("Steps = %d, want 20 (L40→L50 is 20 half-steps)", result.Steps)
	}

	if result.XLStepsIncluded != 20 {
		t.Errorf("XLStepsIncluded = %d, want 20 (entire climb is XL era)", result.XLStepsIncluded)
	}

	if !result.CrossesXLBoundary {
		t.Errorf("CrossesXLBoundary = false, want true")
	}
}

// TestPowerupCost_FullClimbL1toL50 pins the combined L1→L50 total
// at 520,000 (270,000 pre-XL + 250,000 XL era).
func TestPowerupCost_FullClimbL1toL50(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 1.0,
		ToLevel:   50.0,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	const wantStardust = 520000

	if result.StardustCost != wantStardust {
		t.Errorf("StardustCost L1→L50 = %d, want %d", result.StardustCost, wantStardust)
	}

	if result.Steps != 98 {
		t.Errorf("Steps = %d, want 98 (78 pre-XL + 20 XL era)", result.Steps)
	}

	if result.XLStepsIncluded != 20 {
		t.Errorf("XLStepsIncluded = %d, want 20", result.XLStepsIncluded)
	}
}

// TestPowerupCost_XLEraBucketTransitions pins the exact stardust
// tier at each Bulbapedia-published XL-era bucket boundary so a
// future re-sourcing that misplaces a transition is caught.
func TestPowerupCost_XLEraBucketTransitions(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	cases := []struct {
		name         string
		from, to     float64
		wantStardust int
	}{
		// Bucket cost applies to steps STARTING at each half-level
		// in the named range, so e.g. bucket L41.0-L42.5 covers
		// steps starting at 41.0 / 41.5 / 42.0 / 42.5 (4 half-steps
		// ending at L43.0). The "enters N-k tier" marker is the
		// step that STARTS at the first half-level where the new
		// tier kicks in.
		{"L40.5->L41 still 10k tier", 40.5, 41.0, 10000},
		{"L41->L41.5 enters 11k tier", 41.0, 41.5, 11000},
		{"L43->L43.5 enters 12k tier", 43.0, 43.5, 12000},
		{"L45->L45.5 enters 13k tier", 45.0, 45.5, 13000},
		{"L47->L47.5 enters 14k tier", 47.0, 47.5, 14000},
		{"L49->L49.5 enters 15k tier", 49.0, 49.5, 15000},
		{"L49.5->L50 last step 15k", 49.5, 50.0, 15000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
				FromLevel: tc.from,
				ToLevel:   tc.to,
			})
			if err != nil {
				t.Fatalf("handler: %v", err)
			}

			if result.StardustCost != tc.wantStardust {
				t.Errorf("L%.1f→L%.1f StardustCost = %d, want %d",
					tc.from, tc.to, result.StardustCost, tc.wantStardust)
			}
		})
	}
}

// TestPowerupCost_LuckyStardustHalved pins Niantic's 50% stardust
// discount for Lucky Pokémon applied on the full L1→L40 climb:
// 270,000 / 2 = 135,000. Integer division is exact because every
// pre-XL bucket is a multiple of 200 (divisible by 2).
func TestPowerupCost_LuckyStardustHalved(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 1.0,
		ToLevel:   40.0,
		Options:   tools.CombatantOptions{Lucky: true},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	const (
		wantBaseline = 270000
		wantStardust = 135000
	)

	if result.BaselineStardustCost != wantBaseline {
		t.Errorf("BaselineStardustCost = %d, want %d", result.BaselineStardustCost, wantBaseline)
	}

	if result.StardustCost != wantStardust {
		t.Errorf("Lucky StardustCost = %d, want %d", result.StardustCost, wantStardust)
	}

	if result.StardustMultiplier != 0.5 {
		t.Errorf("StardustMultiplier = %v, want 0.5", result.StardustMultiplier)
	}
}

// TestPowerupCost_ShadowStardustPremium pins the Shadow ×1.2
// multiplier on a 10k baseline step: L40.0→L40.5 costs 10000
// baseline, 12000 with Shadow. Uses integer arithmetic ×12/÷10 so
// the result is exact.
func TestPowerupCost_ShadowStardustPremium(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 40.0,
		ToLevel:   40.5,
		Options:   tools.CombatantOptions{Shadow: true},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.StardustCost != 12000 {
		t.Errorf("Shadow StardustCost = %d, want 12000 (10000 × 1.2)", result.StardustCost)
	}

	if result.StardustMultiplier != 1.2 {
		t.Errorf("StardustMultiplier = %v, want 1.2", result.StardustMultiplier)
	}
}

// TestPowerupCost_PurifiedStardustDiscount pins the Purified ×0.9
// multiplier: L40.0→L40.5 costs 10000 baseline, 9000 with Purified.
func TestPowerupCost_PurifiedStardustDiscount(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 40.0,
		ToLevel:   40.5,
		Options:   tools.CombatantOptions{Purified: true},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.StardustCost != 9000 {
		t.Errorf("Purified StardustCost = %d, want 9000 (10000 × 0.9)", result.StardustCost)
	}

	if result.StardustMultiplier != 0.9 {
		t.Errorf("StardustMultiplier = %v, want 0.9", result.StardustMultiplier)
	}
}

// TestPowerupCost_CostMultiplierVsStardustMultiplier pins the
// design rationale for carrying both wire fields: CostMultiplier
// is the "future candy scaler" (Shadow + Purified, Lucky excluded)
// while StardustMultiplier is stardust-only (includes Lucky). The
// two must diverge when Lucky is set alone. A refactor that
// accidentally collapses the two into one field would silently
// regress the response shape; this test fails loudly in that case.
func TestPowerupCost_CostMultiplierVsStardustMultiplier(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	cases := []struct {
		name          string
		opts          tools.CombatantOptions
		wantCostMult  float64
		wantStardMult float64
	}{
		{
			name:          "no flags: both 1.0",
			opts:          tools.CombatantOptions{},
			wantCostMult:  1.0,
			wantStardMult: 1.0,
		},
		{
			name:          "Lucky only: cost 1.0, stardust 0.5 (divergent)",
			opts:          tools.CombatantOptions{Lucky: true},
			wantCostMult:  1.0,
			wantStardMult: 0.5,
		},
		{
			name:          "Shadow only: both 1.2 (convergent when Lucky is off)",
			opts:          tools.CombatantOptions{Shadow: true},
			wantCostMult:  1.2,
			wantStardMult: 1.2,
		},
		{
			name:          "Purified only: both 0.9 (convergent when Lucky is off)",
			opts:          tools.CombatantOptions{Purified: true},
			wantCostMult:  0.9,
			wantStardMult: 0.9,
		},
		{
			name:          "Lucky + Shadow: cost 1.2, stardust 0.6 (divergent)",
			opts:          tools.CombatantOptions{Lucky: true, Shadow: true},
			wantCostMult:  1.2,
			wantStardMult: 0.6,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
				FromLevel: 40.0,
				ToLevel:   40.5,
				Options:   tc.opts,
			})
			if err != nil {
				t.Fatalf("handler: %v", err)
			}

			if math.Abs(result.CostMultiplier-tc.wantCostMult) > 1e-9 {
				t.Errorf("CostMultiplier = %v, want %v", result.CostMultiplier, tc.wantCostMult)
			}

			if math.Abs(result.StardustMultiplier-tc.wantStardMult) > 1e-9 {
				t.Errorf("StardustMultiplier = %v, want %v", result.StardustMultiplier, tc.wantStardMult)
			}
		})
	}
}

// TestPowerupCost_NoteAccurate pins the disclaimer prose against
// two kinds of drift: (1) the old "fails arithmetic" phrasing
// incorrectly accused Bulbapedia of publishing inconsistent
// totals — round-1 review caught that Bulbapedia's candy table
// IS self-consistent (304 = 20+40+30+40+24+32+40+48+30 across 9
// buckets) and the "199" sum was a strawman; (2) the Note must
// mention the cross-source-disagreement rationale that justifies
// deferring candy output. Locking both conditions prevents either
// form of drift.
func TestPowerupCost_NoteAccurate(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 1.0,
		ToLevel:   40.0,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	forbidden := []string{"fails arithmetic", "199"}
	for _, f := range forbidden {
		if strings.Contains(result.Note, f) {
			t.Errorf("Note still contains forbidden phrase %q (Bulbapedia is self-consistent): %q",
				f, result.Note)
		}
	}

	requiredLower := []string{"candy", "self-consistent"}

	noteLower := strings.ToLower(result.Note)
	for _, r := range requiredLower {
		if !strings.Contains(noteLower, r) {
			t.Errorf("Note missing required phrase %q: %q", r, result.Note)
		}
	}
}

// TestPowerupCost_JSONShapeXLEra pins the response JSON shape for
// an XL-era query: crosses_xl_boundary and xl_steps_included are
// non-zero in that case and must appear in the payload. Paired
// with the existing TestPowerupCost_JSONShape (pre-XL path, those
// two keys omitted) to lock both shapes.
func TestPowerupCost_JSONShapeXLEra(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 40.0,
		ToLevel:   41.0,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	decoded := map[string]any{}

	err = json.Unmarshal(payload, &decoded)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	wantKeys := []string{
		"from_level", "to_level", "steps",
		"stardust_cost", "baseline_stardust_cost",
		"cost_multiplier", "stardust_multiplier",
		"crosses_xl_boundary", "xl_steps_included",
		"note",
	}

	gotKeys := make([]string, 0, len(decoded))
	for k := range decoded {
		gotKeys = append(gotKeys, k)
	}

	slices.Sort(wantKeys)
	slices.Sort(gotKeys)

	if !slices.Equal(gotKeys, wantKeys) {
		t.Errorf("XL-era JSON keys = %v, want %v", gotKeys, wantKeys)
	}
}

// TestPowerupCost_LuckyPurifiedStack pins the stacking multiplier:
// Lucky ×0.5 × Purified ×0.9 = 0.45. A 10k baseline step becomes
// 4500 stardust.
func TestPowerupCost_LuckyPurifiedStack(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 40.0,
		ToLevel:   40.5,
		Options:   tools.CombatantOptions{Lucky: true, Purified: true},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.StardustCost != 4500 {
		t.Errorf("Lucky+Purified StardustCost = %d, want 4500 (10000 × 0.45)", result.StardustCost)
	}

	const wantMult = 0.45

	if math.Abs(result.StardustMultiplier-wantMult) > 1e-9 {
		t.Errorf("StardustMultiplier = %v, want %v", result.StardustMultiplier, wantMult)
	}
}
