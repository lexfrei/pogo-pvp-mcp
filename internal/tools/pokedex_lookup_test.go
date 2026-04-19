package tools_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

// speciesScyther and speciesScytherGalarian are hoisted species ids
// used by the sort-ordering assertions in the substring and dex
// tests. goconst flagged repeated literals; the constants keep the
// test body focused on the assertion intent.
const (
	speciesScyther         = "scyther"
	speciesScytherGalarian = "scyther_galarian"
)

// pokedexLookupFixtureGamemaster publishes a small cross-section of
// species covering every query-shape path the tool dispatches on:
//   - dex-only lookups (multiple species share dex 123 via regional
//     variants).
//   - exact-id match (speciesId=farigiraf unique in gamemaster).
//   - substring match across base + regional forms.
//   - shadow filtering (medicham + medicham_shadow published; default
//     query drops the shadow entry).
const pokedexLookupFixtureGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 123, "speciesId": "scyther", "speciesName": "Scyther",
     "baseStats": {"atk": 218, "def": 170, "hp": 172},
     "types": ["bug", "flying"],
     "fastMoves": ["FURY_CUTTER"], "chargedMoves": ["NIGHT_SLASH"],
     "released": true},
    {"dex": 123, "speciesId": "scyther_galarian", "speciesName": "Galarian Scyther",
     "baseStats": {"atk": 218, "def": 170, "hp": 172},
     "types": ["bug", "flying"],
     "fastMoves": ["FURY_CUTTER"], "chargedMoves": ["NIGHT_SLASH"],
     "released": true},
    {"dex": 916, "speciesId": "farigiraf", "speciesName": "Farigiraf",
     "baseStats": {"atk": 170, "def": 144, "hp": 270},
     "types": ["normal", "psychic"],
     "fastMoves": ["CONFUSION"], "chargedMoves": ["PSYCHIC"],
     "released": true},
    {"dex": 308, "speciesId": "medicham", "speciesName": "Medicham",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"],
     "released": true},
    {"dex": 308, "speciesId": "medicham_shadow", "speciesName": "Medicham (Shadow)",
     "baseStats": {"atk": 121, "def": 152, "hp": 155},
     "types": ["fighting", "psychic"],
     "fastMoves": ["COUNTER"], "chargedMoves": ["ICE_PUNCH"],
     "released": true}
  ],
  "moves": [
    {"moveId": "FURY_CUTTER", "name": "Fury Cutter", "type": "bug",
     "power": 3, "energy": 0, "energyGain": 6, "cooldown": 500, "turns": 1},
    {"moveId": "NIGHT_SLASH", "name": "Night Slash", "type": "dark",
     "power": 50, "energy": 35, "cooldown": 500},
    {"moveId": "CONFUSION", "name": "Confusion", "type": "psychic",
     "power": 16, "energy": 0, "energyGain": 4, "cooldown": 1200, "turns": 3},
    {"moveId": "PSYCHIC", "name": "Psychic", "type": "psychic",
     "power": 90, "energy": 55, "cooldown": 500},
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting",
     "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice",
     "power": 55, "energy": 40, "cooldown": 500}
  ]
}`

// TestPokedexLookup_DexNumber pins the all-digit query path: a
// numeric query resolves to Dex matches, returning the base form
// first then variants sorted alphabetically. Shadow variants are
// excluded by default.
func TestPokedexLookup_DexNumber(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, pokedexLookupFixtureGamemaster)
	handler := tools.NewPokedexLookupTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.PokedexLookupParams{Query: "123"})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Matches) != 2 {
		t.Fatalf("Matches len = %d, want 2 (scyther + scyther_galarian)", len(result.Matches))
	}

	if result.Matches[0].SpeciesID != speciesScyther {
		t.Errorf("Matches[0].SpeciesID = %q, want %q (base form must sort first)",
			result.Matches[0].SpeciesID, speciesScyther)
	}

	if result.Matches[1].SpeciesID != speciesScytherGalarian {
		t.Errorf("Matches[1].SpeciesID = %q, want %q",
			result.Matches[1].SpeciesID, speciesScytherGalarian)
	}
}

// TestPokedexLookup_ExactSpeciesID pins the exact-id path: when the
// query matches a species id verbatim, that species comes first in
// the response; substring matches follow.
func TestPokedexLookup_ExactSpeciesID(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, pokedexLookupFixtureGamemaster)
	handler := tools.NewPokedexLookupTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.PokedexLookupParams{Query: "farigiraf"})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Matches) == 0 {
		t.Fatal("Matches empty; want at least farigiraf")
	}

	if result.Matches[0].SpeciesID != "farigiraf" {
		t.Errorf("Matches[0].SpeciesID = %q, want \"farigiraf\" (exact match first)",
			result.Matches[0].SpeciesID)
	}
}

// TestPokedexLookup_Substring pins the substring path for a query
// that is not a dex number and not an exact id: case-insensitive
// match against species id + display name, ordered by dex number.
func TestPokedexLookup_Substring(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, pokedexLookupFixtureGamemaster)
	handler := tools.NewPokedexLookupTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.PokedexLookupParams{Query: "scyth"})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Matches) != 2 {
		t.Fatalf("Matches len = %d, want 2 (both scyther forms)", len(result.Matches))
	}

	// Both entries share the same dex; the substring path sorts
	// by (Dex, SpeciesID) so "scyther" must come before
	// "scyther_galarian" alphabetically. This pins the sort
	// comparator against a silent regression.
	if result.Matches[0].SpeciesID != speciesScyther {
		t.Errorf("Matches[0].SpeciesID = %q, want %q (alphabetical tie-break at same dex)",
			result.Matches[0].SpeciesID, speciesScyther)
	}

	if result.Matches[1].SpeciesID != speciesScytherGalarian {
		t.Errorf("Matches[1].SpeciesID = %q, want %q",
			result.Matches[1].SpeciesID, speciesScytherGalarian)
	}

	for _, m := range result.Matches {
		if m.Dex != 123 {
			t.Errorf("match %q has Dex=%d, want 123", m.SpeciesID, m.Dex)
		}
	}
}

// TestPokedexLookup_ShadowExcludedByDefault pins the default
// behaviour: queries matching both a base form and its _shadow
// variant return only the base form; include_shadow=true restores
// the shadow entry.
func TestPokedexLookup_ShadowExcludedByDefault(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, pokedexLookupFixtureGamemaster)
	handler := tools.NewPokedexLookupTool(mgr).Handler()

	_, withoutShadow, err := handler(t.Context(), nil, tools.PokedexLookupParams{Query: "medicham"})
	if err != nil {
		t.Fatalf("handler (no shadow): %v", err)
	}

	for _, m := range withoutShadow.Matches {
		if m.SpeciesID == speciesMedichamShadow {
			t.Errorf("medicham_shadow leaked into default query result")
		}
	}

	_, withShadow, err := handler(t.Context(), nil, tools.PokedexLookupParams{
		Query:         "medicham",
		IncludeShadow: true,
	})
	if err != nil {
		t.Fatalf("handler (include shadow): %v", err)
	}

	var seenShadow bool

	for _, m := range withShadow.Matches {
		if m.SpeciesID == speciesMedichamShadow {
			seenShadow = true

			break
		}
	}

	if !seenShadow {
		t.Errorf("include_shadow=true did not surface medicham_shadow; matches=%+v",
			withShadow.Matches)
	}
}

// TestPokedexLookup_EmptyQueryRejected pins the guard against empty
// or whitespace-only queries.
func TestPokedexLookup_EmptyQueryRejected(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, pokedexLookupFixtureGamemaster)
	handler := tools.NewPokedexLookupTool(mgr).Handler()

	cases := []string{"", "   ", "\t\n"}
	for _, query := range cases {
		_, _, err := handler(t.Context(), nil, tools.PokedexLookupParams{Query: query})
		if !errors.Is(err, tools.ErrEmptyPokedexQuery) {
			t.Errorf("query %q: error = %v, want wrapping ErrEmptyPokedexQuery", query, err)
		}
	}
}

// TestPokedexLookup_NoMatches pins the empty-result path: a query
// that matches nothing returns an empty Matches slice without error.
func TestPokedexLookup_NoMatches(t *testing.T) {
	t.Parallel()

	mgr := newManagerWithFixture(t, pokedexLookupFixtureGamemaster)
	handler := tools.NewPokedexLookupTool(mgr).Handler()

	_, result, err := handler(t.Context(), nil, tools.PokedexLookupParams{Query: "xyzzy-not-a-species"})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Matches) != 0 {
		t.Errorf("Matches = %+v, want empty for unmatched query", result.Matches)
	}
}

// TestPokedexLookup_GamemasterNotLoaded pins the defensive guard
// against calling the handler before the first gamemaster refresh
// has populated the manager.
func TestPokedexLookup_GamemasterNotLoaded(t *testing.T) {
	t.Parallel()

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    "http://example.invalid",
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	handler := tools.NewPokedexLookupTool(mgr).Handler()

	_, _, err = handler(t.Context(), nil, tools.PokedexLookupParams{Query: "pikachu"})
	if !errors.Is(err, tools.ErrGamemasterNotLoaded) {
		t.Errorf("error = %v, want wrapping ErrGamemasterNotLoaded", err)
	}
}
