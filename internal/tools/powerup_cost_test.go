package tools_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

// TestPowerupCost_L1ToL40 pins the full pre-XL climb: 78 half-level
// steps from L1 to L40 cost a known total of stardust and candy.
// The expected values are computed from the bucket table directly:
//
//	stardust = 4*(200+400+600+800+1000+1300+1600+1900+2200+2500+
//	            3000+3500+4000+4500+5000+6000+7000+8000+9000)
//	         + 2*10000
//	         = 270,000
//	candy    = 4*(1+1+1+1+1+2+2+2+2+2+3+3+4+4+6+8+10+12+15)
//	         + 2*15
//	         = 350
//
// These totals are published by Pokémon GO Hub and Bulbapedia; if a
// future Niantic rework changes any bucket this test breaks
// immediately.
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

	if result.CandyCost != 350 {
		t.Errorf("CandyCost = %d, want 350", result.CandyCost)
	}
}

// TestPowerupCost_SingleStep pins one step: L1.0 → L1.5 costs
// 200 stardust, 1 candy.
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

	if result.CandyCost != 1 {
		t.Errorf("CandyCost = %d, want 1", result.CandyCost)
	}
}

// TestPowerupCost_BucketBoundaries pins three cases on the bucket
// edges: the last L29-L30.5 step (5000 dust + 6 candy) and the
// final L39.5→L40 step (10000 dust + 15 candy). These values are
// the stress test of bucket-boundary indexing.
func TestPowerupCost_BucketBoundaries(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	cases := []struct {
		name         string
		fromLevel    float64
		toLevel      float64
		wantStardust int
		wantCandy    int
	}{
		// Price buckets are keyed on the STARTING level, covering 4
		// half-level steps: e.g. the 5000-dust bucket's starting
		// levels are L29, L29.5, L30, L30.5 — so the step FROM L30.5
		// (ending at L31) still pays the 5000-dust rate.
		{"L29.0-L29.5 (first step of 5000/6 bucket)", 29.0, 29.5, 5000, 6},
		{"L30.5-L31.0 (last step of 5000/6 bucket)", 30.5, 31.0, 5000, 6},
		{"L31.0-L31.5 (first step of 6000/8 bucket)", 31.0, 31.5, 6000, 8},
		{"L39.5-L40.0 (final pre-XL step)", 39.5, 40.0, 10000, 15},
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

			if result.CandyCost != tc.wantCandy {
				t.Errorf("CandyCost = %d, want %d", result.CandyCost, tc.wantCandy)
			}
		})
	}
}

// TestPowerupCost_XLRangeRejected pins the explicit refusal of
// post-L40 queries (XL candy era). Niantic has shifted those values
// across adjustments; we refuse rather than hand the caller stale
// numbers.
func TestPowerupCost_XLRangeRejected(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 40.0,
		ToLevel:   40.5,
	})
	if !errors.Is(err, tools.ErrXLRangeNotSupported) {
		t.Errorf("error = %v, want wrapping ErrXLRangeNotSupported", err)
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

	if whole.CandyCost != left.CandyCost+right.CandyCost {
		t.Errorf("candy sum mismatch: whole=%d, left+right=%d",
			whole.CandyCost, left.CandyCost+right.CandyCost)
	}
}

// TestPowerupCost_DescriptionSanity locks the XL-exclusion
// disclaimer into the MCP tool description so LLM clients reading
// the schema cannot miss it.
func TestPowerupCost_DescriptionSanity(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	desc := tool.Tool().Description

	if desc == "" {
		t.Fatal("description is empty")
	}

	descLower := strings.ToLower(desc)

	wantFragments := []string{"l1-l40", "xl", "pre-xl"}
	for _, frag := range wantFragments {
		if !strings.Contains(descLower, frag) {
			t.Errorf("description missing fragment %q; got %q", frag, desc)
		}
	}
}
