package tools_test

import (
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

// TestRank_CupFilterReturnsOnlyNamedCup pins R6.5: passing the
// new Cup param narrows RankingsByCup to that single cup (here
// "spring"), instead of emitting every published cup like the
// default empty-cup behaviour.
func TestRank_CupFilterReturnsOnlyNamedCup(t *testing.T) {
	t.Parallel()

	const openPayload = `[
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 700,
   "moveset": ["COUNTER", "ICE_PUNCH", "PSYCHIC"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}}
]`

	const springPayload = `[
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 820,
   "moveset": ["COUNTER", "PSYCHIC", "ICE_PUNCH"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}}
]`

	gm := newManagerWithFixture(t, rankMultiCupFixtureGamemaster)
	ranks := newRankingsManagerMultiCup(t, openPayload, springPayload)

	handler := tools.NewRankTool(gm, ranks).Handler()

	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: speciesMedicham,
		IV:      [3]int{0, 15, 15},
		League:  leagueGreat,
		Cup:     cupSpringLabel,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.RankingsByCup) != 1 {
		t.Fatalf("RankingsByCup len = %d, want 1 (single-cup filter)", len(result.RankingsByCup))
	}

	if result.RankingsByCup[0].Cup != cupSpringLabel {
		t.Errorf("RankingsByCup[0].Cup = %q, want %q",
			result.RankingsByCup[0].Cup, cupSpringLabel)
	}
}

// TestRank_CupFilterEmptyReturnsAll pins the regression: omitting
// the Cup param keeps the pre-R6.5 contract of emitting every
// published cup the species appears in (open-league + spring in
// this fixture).
func TestRank_CupFilterEmptyReturnsAll(t *testing.T) {
	t.Parallel()

	const openPayload = `[
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 700,
   "moveset": ["COUNTER", "ICE_PUNCH", "PSYCHIC"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}}
]`

	const springPayload = `[
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 820,
   "moveset": ["COUNTER", "PSYCHIC", "ICE_PUNCH"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}}
]`

	gm := newManagerWithFixture(t, rankMultiCupFixtureGamemaster)
	ranks := newRankingsManagerMultiCup(t, openPayload, springPayload)

	handler := tools.NewRankTool(gm, ranks).Handler()

	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: speciesMedicham,
		IV:      [3]int{0, 15, 15},
		League:  leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.RankingsByCup) != 2 {
		t.Fatalf("RankingsByCup len = %d, want 2 (regression: empty filter still emits all cups)",
			len(result.RankingsByCup))
	}
}

// TestRank_CupFilterUnknownCupReturnsEmpty pins the behaviour for
// a Cup that does not exist in the gamemaster: silent empty slice
// instead of an error, consistent with the existing "cup not
// published by pvpoke" tolerance elsewhere in the pipeline.
func TestRank_CupFilterUnknownCupReturnsEmpty(t *testing.T) {
	t.Parallel()

	const openPayload = `[
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 700,
   "moveset": ["COUNTER", "ICE_PUNCH", "PSYCHIC"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}}
]`

	gm := newManagerWithFixture(t, rankMultiCupFixtureGamemaster)
	ranks := newRankingsManagerMultiCup(t, openPayload, "[]")

	handler := tools.NewRankTool(gm, ranks).Handler()

	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: speciesMedicham,
		IV:      [3]int{0, 15, 15},
		League:  leagueGreat,
		Cup:     "nonexistent-cup-id",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.RankingsByCup) != 0 {
		t.Errorf("RankingsByCup = %+v, want empty (unknown cup → silent empty)",
			result.RankingsByCup)
	}
}

// TestRank_CupFilterAllStringReturnsOpenLeagueOnly pins: passing
// Cup="all" narrows rankings_by_cup to the single open-league row.
// Complements TestRank_CupFilterEmptyReturnsAll (which asserts
// Cup="" emits every published cup) — the two inputs deliberately
// produce different output shapes, and callers who want the pre-
// R6.5 "every cup" behaviour must leave Cup empty, not set it to
// "all".
func TestRank_CupFilterAllStringReturnsOpenLeagueOnly(t *testing.T) {
	t.Parallel()

	const openPayload = `[
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 700,
   "moveset": ["COUNTER", "ICE_PUNCH", "PSYCHIC"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}}
]`

	const springPayload = `[
  {"speciesId": "medicham", "speciesName": "Medicham", "rating": 820,
   "moveset": ["COUNTER", "PSYCHIC", "ICE_PUNCH"],
   "stats": {"product": 2100, "atk": 106, "def": 139, "hp": 141}}
]`

	gm := newManagerWithFixture(t, rankMultiCupFixtureGamemaster)
	ranks := newRankingsManagerMultiCup(t, openPayload, springPayload)

	handler := tools.NewRankTool(gm, ranks).Handler()

	_, result, err := handler(t.Context(), nil, tools.RankParams{
		Species: speciesMedicham,
		IV:      [3]int{0, 15, 15},
		League:  leagueGreat,
		Cup:     cupAllLabel,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.RankingsByCup) != 1 {
		t.Fatalf("RankingsByCup len = %d, want 1 (Cup=all must resolve to open-league slice only)",
			len(result.RankingsByCup))
	}

	if result.RankingsByCup[0].Cup != cupAllLabel {
		t.Errorf("RankingsByCup[0].Cup = %q, want %q",
			result.RankingsByCup[0].Cup, cupAllLabel)
	}
}
