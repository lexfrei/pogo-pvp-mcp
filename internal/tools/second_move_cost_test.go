package tools_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

const secondMoveCostFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 308, "speciesId": "medicham", "speciesName": "Medicham",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"],
     "thirdMoveCost": 50000, "buddyDistance": 3,
     "released": true},
    {"dex": 308, "speciesId": "medicham_shadow", "speciesName": "Medicham (Shadow)",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"],
     "thirdMoveCost": 50000, "buddyDistance": 3,
     "released": true},
    {"dex": 340, "speciesId": "whiscash", "speciesName": "Whiscash",
     "baseStats": {"atk": 151, "def": 141, "hp": 242},
     "types": ["water", "ground"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"],
     "thirdMoveCost": 10000, "buddyDistance": 1,
     "released": true},
    {"dex": 132, "speciesId": "ditto", "speciesName": "Ditto",
     "baseStats": {"atk": 91, "def": 91, "hp": 134},
     "types": ["normal"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"],
     "released": true}
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "cooldown": 500}
  ]
}`

func newSecondMoveCostTool(t *testing.T) *tools.SecondMoveCostTool {
	t.Helper()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(secondMoveCostFixture))
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

	return tools.NewSecondMoveCostTool(gmMgr)
}

// TestSecondMoveCost_3kmBuddy pins medicham: 50,000 stardust + 50
// candy (3km buddy). The round-1 review blocker was that the prior
// implementation reported CandyCost=50000 (equal to stardust) —
// factually wrong. This test fails against that bug.
func TestSecondMoveCost_3kmBuddy(t *testing.T) {
	t.Parallel()

	tool := newSecondMoveCostTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.SecondMoveCostParams{
		Species: speciesMedicham,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.StardustCost != 50000 {
		t.Errorf("StardustCost = %d, want 50000", result.StardustCost)
	}

	if result.CandyCost != 50 {
		t.Errorf("CandyCost = %d, want 50 (3km buddy distance)", result.CandyCost)
	}

	if result.BuddyDistanceKM != 3 {
		t.Errorf("BuddyDistanceKM = %d, want 3", result.BuddyDistanceKM)
	}

	if !result.StardustCostAvailable || !result.CandyCostAvailable {
		t.Errorf("availability flags both should be true: stardust=%v candy=%v",
			result.StardustCostAvailable, result.CandyCostAvailable)
	}

	if result.CostMultiplier != 1.0 {
		t.Errorf("CostMultiplier = %f, want 1.0 (non-shadow species)", result.CostMultiplier)
	}
}

// TestSecondMoveCost_1kmBuddy pins the 1km buddy rate via whiscash:
// 10,000 stardust + 25 candy.
func TestSecondMoveCost_1kmBuddy(t *testing.T) {
	t.Parallel()

	tool := newSecondMoveCostTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.SecondMoveCostParams{
		Species: "whiscash",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.StardustCost != 10000 {
		t.Errorf("StardustCost = %d, want 10000", result.StardustCost)
	}

	if result.CandyCost != 25 {
		t.Errorf("CandyCost = %d, want 25 (1km buddy distance)", result.CandyCost)
	}

	if result.BuddyDistanceKM != 1 {
		t.Errorf("BuddyDistanceKM = %d, want 1", result.BuddyDistanceKM)
	}
}

// TestSecondMoveCost_ShadowMultiplier pins the 1.2× shadow penalty
// on both currencies. medicham_shadow is the same base (3km buddy,
// 50,000 stardust) so the response must multiply by 1.2 →
// 60,000 stardust + 60 candy. (Niantic-documented; symmetric with
// the purified 0.8× discount. Round 2 review caught an earlier
// 3× misread.)
func TestSecondMoveCost_ShadowMultiplier(t *testing.T) {
	t.Parallel()

	tool := newSecondMoveCostTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.SecondMoveCostParams{
		Species: speciesMedicham,
		Options: tools.CombatantOptions{Shadow: true},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.CostMultiplier != 1.2 {
		t.Errorf("CostMultiplier = %f, want 1.2", result.CostMultiplier)
	}

	if result.StardustCost != 60000 {
		t.Errorf("StardustCost = %d, want 60000 (1.2× shadow)", result.StardustCost)
	}

	if result.CandyCost != 60 {
		t.Errorf("CandyCost = %d, want 60 (1.2× shadow)", result.CandyCost)
	}

	if result.Note == "" {
		t.Errorf("Note empty on shadow response; disclaimer must be carried")
	}

	if strings.Contains(result.Note, "re-query the purified species id") {
		t.Errorf("Note still contains stale \"re-query the purified species id\" advice: %q",
			result.Note)
	}
}

// TestSecondMoveCost_MissingBuddyDistance pins the degraded path:
// ditto in the fixture has neither thirdMoveCost nor buddyDistance
// in the payload, so CandyCostAvailable must be false and
// StardustCostAvailable likewise.
func TestSecondMoveCost_MissingBuddyDistance(t *testing.T) {
	t.Parallel()

	tool := newSecondMoveCostTool(t)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.SecondMoveCostParams{
		Species: "ditto",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.CandyCostAvailable {
		t.Errorf("CandyCostAvailable = true; ditto has no buddyDistance in fixture")
	}

	if result.StardustCostAvailable {
		t.Errorf("StardustCostAvailable = true; ditto has no thirdMoveCost in fixture")
	}

	if result.BuddyDistanceKM != 0 {
		t.Errorf("BuddyDistanceKM = %d, want 0 (omitempty on wire; no upstream data)",
			result.BuddyDistanceKM)
	}

	if result.Note == "" {
		t.Errorf("Note empty on missing-data response; caller needs the explanation")
	}
}

// TestSecondMoveCost_ToolDescriptionSanity pins the multiplier
// phrasing in the MCP tool description the LLM client reads. An
// earlier iteration let the description say "3× both currencies"
// after the code had already been corrected to 1.2× — locking this
// substring prevents a regression where code and description drift.
func TestSecondMoveCost_ToolDescriptionSanity(t *testing.T) {
	t.Parallel()

	tool := tools.NewSecondMoveCostTool(nil)
	desc := tool.Tool().Description

	if strings.Contains(desc, "3×") || strings.Contains(desc, "3x") {
		t.Errorf("description still contains a 3× shadow claim: %q", desc)
	}

	if !strings.Contains(desc, "1.2") {
		t.Errorf("description missing the 1.2× shadow multiplier: %q", desc)
	}
}

// TestSecondMoveCost_AllBuddyBrackets sweeps every canonical
// buddy-km bracket through the tool and asserts the candy cost
// matches the Niantic table. The round-2 review caught that only
// 1km and 3km had coverage; this test closes 5km and 20km.
func TestSecondMoveCost_AllBuddyBrackets(t *testing.T) {
	t.Parallel()

	const fixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 1, "speciesId": "buddy1", "speciesName": "Buddy1",
     "baseStats": {"atk": 100, "def": 100, "hp": 100}, "types": ["normal"],
     "fastMoves": ["TACKLE"], "chargedMoves": ["BODY_SLAM"],
     "thirdMoveCost": 10000, "buddyDistance": 1, "released": true},
    {"dex": 2, "speciesId": "buddy3", "speciesName": "Buddy3",
     "baseStats": {"atk": 100, "def": 100, "hp": 100}, "types": ["normal"],
     "fastMoves": ["TACKLE"], "chargedMoves": ["BODY_SLAM"],
     "thirdMoveCost": 50000, "buddyDistance": 3, "released": true},
    {"dex": 3, "speciesId": "buddy5", "speciesName": "Buddy5",
     "baseStats": {"atk": 100, "def": 100, "hp": 100}, "types": ["normal"],
     "fastMoves": ["TACKLE"], "chargedMoves": ["BODY_SLAM"],
     "thirdMoveCost": 75000, "buddyDistance": 5, "released": true},
    {"dex": 4, "speciesId": "buddy20", "speciesName": "Buddy20",
     "baseStats": {"atk": 100, "def": 100, "hp": 100}, "types": ["normal"],
     "fastMoves": ["TACKLE"], "chargedMoves": ["BODY_SLAM"],
     "thirdMoveCost": 100000, "buddyDistance": 20, "released": true}
  ],
  "moves": [
    {"moveId": "TACKLE", "name": "Tackle", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 3, "cooldown": 500, "turns": 1},
    {"moveId": "BODY_SLAM", "name": "Body Slam", "type": "normal",
     "power": 60, "energy": 35, "cooldown": 500}
  ]
}`

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fixture))
	}))
	t.Cleanup(gmServer.Close)

	gmMgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    gmServer.URL,
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	err = gmMgr.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	tool := tools.NewSecondMoveCostTool(gmMgr)
	handler := tool.Handler()

	cases := []struct {
		species      string
		wantStardust int
		wantCandy    int
		wantBuddyKM  int
	}{
		{"buddy1", 10000, 25, 1},
		{"buddy3", 50000, 50, 3},
		{"buddy5", 75000, 75, 5},
		{"buddy20", 100000, 100, 20},
	}

	for _, tc := range cases {
		t.Run(tc.species, func(t *testing.T) {
			t.Parallel()

			_, result, err := handler(t.Context(), nil, tools.SecondMoveCostParams{
				Species: tc.species,
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

			if result.BuddyDistanceKM != tc.wantBuddyKM {
				t.Errorf("BuddyDistanceKM = %d, want %d",
					result.BuddyDistanceKM, tc.wantBuddyKM)
			}
		})
	}
}

// TestSecondMoveCost_ShadowWithMissingBuddyDistance pins the
// compose-both-notes path: a shadow species whose payload publishes
// stardust but not buddyDistance must carry BOTH the shadow-premium
// disclaimer AND the candy-unavailable caveat in the same Note.
// Round-2 review caught a switch that treated them as mutually
// exclusive, dropping the shadow note entirely on partial data.
func TestSecondMoveCost_ShadowWithMissingBuddyDistance(t *testing.T) {
	t.Parallel()

	const fixtureWithShadowNoBuddy = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 150, "speciesId": "mewtwo_shadow", "speciesName": "Mewtwo (Shadow)",
     "baseStats": {"atk": 300, "def": 182, "hp": 214},
     "types": ["psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"],
     "thirdMoveCost": 75000,
     "released": true}
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "cooldown": 500}
  ]
}`

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fixtureWithShadowNoBuddy))
	}))
	t.Cleanup(gmServer.Close)

	gmMgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    gmServer.URL,
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	err = gmMgr.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	tool := tools.NewSecondMoveCostTool(gmMgr)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.SecondMoveCostParams{
		Species: "mewtwo",
		Options: tools.CombatantOptions{Shadow: true},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.CostMultiplier != 1.2 {
		t.Errorf("CostMultiplier = %f, want 1.2", result.CostMultiplier)
	}

	if result.StardustCost != 90000 {
		t.Errorf("StardustCost = %d, want 90000 (75000 × 1.2)", result.StardustCost)
	}

	if result.CandyCostAvailable {
		t.Errorf("CandyCostAvailable = true; fixture has no buddyDistance")
	}

	if !strings.Contains(result.Note, "Shadow") {
		t.Errorf("Note missing shadow disclaimer; got %q", result.Note)
	}

	if !strings.Contains(result.Note, "buddy distance") {
		t.Errorf("Note missing buddy-distance caveat; got %q", result.Note)
	}
}

// TestSecondMoveCost_UnknownSpecies rejects unknown ids.
func TestSecondMoveCost_UnknownSpecies(t *testing.T) {
	t.Parallel()

	tool := newSecondMoveCostTool(t)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.SecondMoveCostParams{Species: "missingno"})
	if !errors.Is(err, tools.ErrUnknownSpecies) {
		t.Errorf("error = %v, want wrapping ErrUnknownSpecies", err)
	}
}

// TestSecondMoveCost_EmptySpecies rejects empty species early.
func TestSecondMoveCost_EmptySpecies(t *testing.T) {
	t.Parallel()

	tool := newSecondMoveCostTool(t)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.SecondMoveCostParams{Species: ""})
	if !errors.Is(err, tools.ErrUnknownSpecies) {
		t.Errorf("error = %v, want wrapping ErrUnknownSpecies", err)
	}
}

// TestSecondMoveCost_GamemasterNotLoaded pins the cold-start sentinel.
func TestSecondMoveCost_GamemasterNotLoaded(t *testing.T) {
	t.Parallel()

	gmMgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    "http://127.0.0.1:1",
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	tool := tools.NewSecondMoveCostTool(gmMgr)
	handler := tool.Handler()

	_, _, err = handler(t.Context(), nil, tools.SecondMoveCostParams{Species: speciesMedicham})
	if !errors.Is(err, tools.ErrGamemasterNotLoaded) {
		t.Errorf("error = %v, want wrapping ErrGamemasterNotLoaded", err)
	}
}
