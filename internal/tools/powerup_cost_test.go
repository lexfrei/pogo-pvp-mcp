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

// TestPowerupCost_XLRangeRejected pins the explicit refusal of
// post-L40 queries (XL candy era).
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

// TestPowerupCost_XLRangeFromLevelRejected also refuses a climb
// whose starting level is past L40.
func TestPowerupCost_XLRangeFromLevelRejected(t *testing.T) {
	t.Parallel()

	tool := tools.NewPowerupCostTool()
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.PowerupCostParams{
		FromLevel: 41.0,
		ToLevel:   41.5,
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

	wantKeys := []string{"from_level", "to_level", "steps", "stardust_cost", "note"}

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

	wantFragments := []string{"l1-l40", "xl", "candy"}
	for _, frag := range wantFragments {
		if !strings.Contains(descLower, frag) {
			t.Errorf("description missing fragment %q; got %q", frag, desc)
		}
	}
}
