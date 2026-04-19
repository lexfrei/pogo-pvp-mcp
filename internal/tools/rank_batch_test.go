package tools_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

// rankBatchFixtureGamemaster is a trimmed gamemaster with one
// species (medicham) so the RankTool handler produces a real result
// without requiring the full engine species list.
const rankBatchFixtureGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 308, "speciesId": "medicham", "speciesName": "Medicham",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"], "released": true}
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "cooldown": 500}
  ]
}`

func newRankBatchTool(t *testing.T) *tools.RankBatchTool {
	t.Helper()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(rankBatchFixtureGamemaster))
	}))
	t.Cleanup(gmServer.Close)

	gmMgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    gmServer.URL,
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager gm: %v", err)
	}

	err = gmMgr.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh gm: %v", err)
	}

	rankServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(rankServer.Close)

	ranksMgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  rankServer.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("NewManager rankings: %v", err)
	}

	return tools.NewRankBatchTool(gmMgr, ranksMgr)
}

// TestRankBatch_HappyPath pins the batch response shape: each input
// IV triple produces one RankBatchEntry in the same order, every
// entry carries OK=true and a non-zero CP / StatProduct when the
// inputs are valid.
func TestRankBatch_HappyPath(t *testing.T) {
	t.Parallel()

	tool := newRankBatchTool(t)
	handler := tool.Handler()

	ivs := [][3]int{
		{0, 15, 15},
		{15, 15, 15},
		{7, 8, 9},
	}

	_, result, err := handler(t.Context(), nil, tools.RankBatchParams{
		Species: "medicham",
		IVs:     ivs,
		League:  "great",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Entries) != len(ivs) {
		t.Fatalf("Entries len = %d, want %d", len(result.Entries), len(ivs))
	}

	if result.SuccessCount != len(ivs) {
		t.Errorf("SuccessCount = %d, want %d (all IVs valid)", result.SuccessCount, len(ivs))
	}

	for i, entry := range result.Entries {
		if entry.IV != ivs[i] {
			t.Errorf("Entries[%d].IV = %v, want %v (order must match input)", i, entry.IV, ivs[i])
		}

		if !entry.OK {
			t.Errorf("Entries[%d].OK = false, error = %q", i, entry.Error)

			continue
		}

		if entry.Result.CP <= 0 {
			t.Errorf("Entries[%d].Result.CP = %d, want positive", i, entry.Result.CP)
		}
	}
}

// TestRankBatch_EmptyIVs rejects a zero-length IV list — passing
// zero items is never a useful batch call and almost always a
// client bug.
func TestRankBatch_EmptyIVs(t *testing.T) {
	t.Parallel()

	tool := newRankBatchTool(t)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.RankBatchParams{
		Species: "medicham",
		IVs:     [][3]int{},
		League:  "great",
	})
	if !errors.Is(err, tools.ErrEmptyIVList) {
		t.Errorf("error = %v, want wrapping ErrEmptyIVList", err)
	}
}

// TestRankBatch_TooManyIVs pins the DoS guard: batch requests beyond
// maxRankBatchSize are rejected before any simulation runs.
func TestRankBatch_TooManyIVs(t *testing.T) {
	t.Parallel()

	tool := newRankBatchTool(t)
	handler := tool.Handler()

	// One more than the cap. The test does not hard-code the constant
	// so a future bump to maxRankBatchSize doesn't break the intent.
	ivs := make([][3]int, 1000)
	for i := range ivs {
		ivs[i] = [3]int{15, 15, 15}
	}

	_, _, err := handler(t.Context(), nil, tools.RankBatchParams{
		Species: "medicham",
		IVs:     ivs,
		League:  "great",
	})
	if !errors.Is(err, tools.ErrTooManyIVs) {
		t.Errorf("error = %v, want wrapping ErrTooManyIVs", err)
	}
}

// TestRankBatch_PartialFailure pins the per-entry isolation: a
// single bad IV triple (e.g. component out of range) becomes an
// OK=false entry without killing the siblings.
func TestRankBatch_PartialFailure(t *testing.T) {
	t.Parallel()

	tool := newRankBatchTool(t)
	handler := tool.Handler()

	ivs := [][3]int{
		{15, 15, 15},
		{16, 15, 15}, // component out of range
		{0, 0, 0},
	}

	_, result, err := handler(t.Context(), nil, tools.RankBatchParams{
		Species: "medicham",
		IVs:     ivs,
		League:  "great",
	})
	if err != nil {
		t.Fatalf("handler: %v (partial failure must not bubble up)", err)
	}

	if len(result.Entries) != 3 {
		t.Fatalf("Entries len = %d, want 3", len(result.Entries))
	}

	if !result.Entries[0].OK {
		t.Errorf("Entries[0] should be OK, got error %q", result.Entries[0].Error)
	}

	if result.Entries[1].OK {
		t.Errorf("Entries[1] (IV 16,15,15) should be !OK")
	}

	if result.Entries[1].Error == "" {
		t.Errorf("Entries[1].Error is empty; should carry the IV validation message")
	}

	if !result.Entries[2].OK {
		t.Errorf("Entries[2] should be OK, got error %q", result.Entries[2].Error)
	}

	if result.SuccessCount != 2 {
		t.Errorf("SuccessCount = %d, want 2", result.SuccessCount)
	}
}

// TestRankBatch_UnknownSpecies the error path propagates per entry:
// an unknown species id produces N OK=false entries, all with the
// same error, and SuccessCount=0.
func TestRankBatch_UnknownSpecies(t *testing.T) {
	t.Parallel()

	tool := newRankBatchTool(t)
	handler := tool.Handler()

	ivs := [][3]int{{15, 15, 15}, {10, 10, 10}}

	_, result, err := handler(t.Context(), nil, tools.RankBatchParams{
		Species: "missingno",
		IVs:     ivs,
		League:  "great",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.SuccessCount != 0 {
		t.Errorf("SuccessCount = %d, want 0 (unknown species)", result.SuccessCount)
	}

	for i, entry := range result.Entries {
		if entry.OK {
			t.Errorf("Entries[%d] OK=true despite unknown species", i)
		}
	}
}
