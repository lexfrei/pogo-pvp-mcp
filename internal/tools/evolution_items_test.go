package tools

import "testing"

// The evolution-items tests live in the `tools` package (not
// `tools_test`) so they can reach the package-private helpers
// directly; this table is an internal implementation detail and
// there is no user-facing API to exercise it from outside.

const testItemSunStone = "sun_stone"

// TestEvolutionRequirementFor_Table exhaustively pins every
// reachable entry. The table is small (19 keys), human-maintained,
// and Niantic changes these values rarely — so locking each entry
// by ID + Item + Candy catches the typo-in-review-fix class of
// regression that bit huntail/gorebyss/espeon/umbreon/magnezone/
// probopass in the initial R6.7 commit.
func TestEvolutionRequirementFor_Table(t *testing.T) {
	t.Parallel()

	cases := []struct {
		species string
		item    string
		candy   int
	}{
		// Gloom split.
		{speciesVileplume, "", evolveCandy100},
		{speciesBellossom, itemSunStone, evolveCandy100},
		// Slowpoke split.
		{speciesSlowbro, "", evolveCandy50},
		{speciesSlowking, itemKingRock, evolveCandy50},
		// Poliwhirl split.
		{speciesPoliwrath, "", evolveCandy100},
		{speciesPolitoed, itemKingRock, evolveCandy100},
		// Clamperl split (random pick, no item in GO).
		{speciesHuntail, "", evolveCandy50},
		{speciesGorebyss, "", evolveCandy50},
		// Eevee branches.
		{speciesVaporeon, "", evolveCandy25},
		{speciesJolteon, "", evolveCandy25},
		{speciesFlareon, "", evolveCandy25},
		{speciesEspeon, "", evolveCandy25},
		{speciesUmbreon, "", evolveCandy25},
		{speciesLeafeon, itemMossyLure, evolveCandy25},
		{speciesGlaceon, itemGlacialLure, evolveCandy25},
		{speciesSylveon, "", evolveCandy25},
		// Tyrogue split.
		{speciesHitmonlee, "", evolveCandy25},
		{speciesHitmonchan, "", evolveCandy25},
		{speciesHitmontop, "", evolveCandy25},
		// Linear item-gated (R7.P2).
		{speciesSunflora, testItemSunStone, evolveCandy50},
		{speciesKingdra, itemDragonScale, evolveCandy100},
		{speciesScizor, itemMetalCoat, evolveCandy50},
		{speciesSteelix, itemMetalCoat, evolveCandy50},
		{speciesPorygon2, itemUpGrade, evolveCandy50},
		{speciesPorygonZ, itemSinnohStone, evolveCandy100},
		{speciesRhyperior, itemSinnohStone, evolveCandy100},
		{speciesElectivire, itemSinnohStone, evolveCandy100},
		{speciesMagmortar, itemSinnohStone, evolveCandy100},
		{speciesGliscor, itemSinnohStone, evolveCandy100},
		{speciesDusknoir, itemSinnohStone, evolveCandy100},
		{speciesTogekiss, itemSinnohStone, evolveCandy100},
		{speciesMagnezone, itemMagneticLure, evolveCandy100},
		{speciesProbopass, itemMagneticLure, evolveCandy50},
	}

	for _, tc := range cases {
		t.Run(tc.species, func(t *testing.T) {
			t.Parallel()

			req := evolutionRequirementFor(tc.species)
			if req == nil {
				t.Fatalf("evolutionRequirementFor(%q) = nil, want populated", tc.species)
			}
			if req.Item != tc.item {
				t.Errorf("Item = %q, want %q", req.Item, tc.item)
			}
			if req.Candy != tc.candy {
				t.Errorf("Candy = %d, want %d", req.Candy, tc.candy)
			}
		})
	}
}

// TestEvolutionRequirementFor_BellossomNeedsSunStone pins the
// Sun-Stone branch Bulbapedia documents for gloom → bellossom.
// Keeping this as a named test (in addition to the table-driven
// coverage) so a failure message surfaces the exact species name.
func TestEvolutionRequirementFor_BellossomNeedsSunStone(t *testing.T) {
	t.Parallel()

	req := evolutionRequirementFor("bellossom")
	if req == nil {
		t.Fatal("evolutionRequirementFor(bellossom) = nil, want populated")
	}
	if req.Item != testItemSunStone {
		t.Errorf("Item = %q, want %s", req.Item, testItemSunStone)
	}
	if req.Candy != evolveCandy100 {
		t.Errorf("Candy = %d, want %d", req.Candy, evolveCandy100)
	}
}

// TestEvolutionRequirementFor_UnknownReturnsNil pins the fall-
// through for species outside the curated table — linear chains
// without an item gate (ivysaur → venusaur), terminal species, and
// out-of-GO chains (scyther → kleavor). Callers should treat nil
// as "consult your own data source" rather than "no requirement".
func TestEvolutionRequirementFor_UnknownReturnsNil(t *testing.T) {
	t.Parallel()

	for _, id := range []string{
		"ivysaur", "venusaur", "ditto", "kleavor",
		"completely-bogus-species",
	} {
		req := evolutionRequirementFor(id)
		if req != nil {
			t.Errorf("evolutionRequirementFor(%q) = %+v, want nil", id, req)
		}
	}
}

// TestEvolutionRequirementFor_ShadowSuffixStripped pins that
// legacy-convention shadow ids resolve to the non-shadow table
// entry — scizor_shadow → Metal Coat, same as scizor. Direct
// unit-level coverage for the suffix-strip in evolutionRequirementFor
// so a regression fails here instead of only in the end-to-end
// team_builder shadow test (tighter failure localisation).
func TestEvolutionRequirementFor_ShadowSuffixStripped(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		shadow string
		base   string
	}{
		{"scizor_shadow", "scizor"},
		{"steelix_shadow", "steelix"},
		{"magnezone_shadow", "magnezone"},
	} {
		t.Run(tc.shadow, func(t *testing.T) {
			t.Parallel()

			got := evolutionRequirementFor(tc.shadow)
			want := evolutionRequirementFor(tc.base)

			if got == nil {
				t.Fatalf("evolutionRequirementFor(%q) = nil, want non-nil via shadow-suffix strip", tc.shadow)
			}
			if want == nil {
				t.Fatalf("base %q missing from table — fix the test fixture", tc.base)
			}
			if got.Item != want.Item || got.Candy != want.Candy {
				t.Errorf("shadow lookup = {item:%q, candy:%d}, want {item:%q, candy:%d}",
					got.Item, got.Candy, want.Item, want.Candy)
			}
		})
	}
}

// TestEvolutionRequirementFor_ReturnsCopy pins that the helper
// hands back an independent struct — caller mutations must not
// pollute the shared table. Verified by mutating the result and
// re-querying.
func TestEvolutionRequirementFor_ReturnsCopy(t *testing.T) {
	t.Parallel()

	first := evolutionRequirementFor("bellossom")
	if first == nil {
		t.Fatal("first lookup = nil")
	}

	first.Item = "MUTATED"
	first.Candy = 9999

	second := evolutionRequirementFor("bellossom")
	if second == nil {
		t.Fatal("second lookup = nil")
	}

	if second.Item != testItemSunStone || second.Candy != evolveCandy100 {
		t.Errorf("shared table mutated: second = %+v, want {%s, %d}",
			second, testItemSunStone, evolveCandy100)
	}
}
