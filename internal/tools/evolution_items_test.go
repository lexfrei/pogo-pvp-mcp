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
		{"vileplume", "", evolveCandy100},
		{"bellossom", "sun_stone", evolveCandy100},
		// Slowpoke split.
		{"slowbro", "", evolveCandy50},
		{"slowking", "king_rock", evolveCandy50},
		// Poliwhirl split.
		{"poliwrath", "", evolveCandy100},
		{"politoed", "king_rock", evolveCandy100},
		// Clamperl split (random pick, no item in GO).
		{"huntail", "", evolveCandy50},
		{"gorebyss", "", evolveCandy50},
		// Eevee branches.
		{"vaporeon", "", evolveCandy25},
		{"jolteon", "", evolveCandy25},
		{"flareon", "", evolveCandy25},
		{"espeon", "", evolveCandy25},
		{"umbreon", "", evolveCandy25},
		{"leafeon", "mossy_lure", evolveCandy25},
		{"glaceon", "glacial_lure", evolveCandy25},
		{"sylveon", "", evolveCandy25},
		// Tyrogue split.
		{"hitmonlee", "", evolveCandy25},
		{"hitmonchan", "", evolveCandy25},
		{"hitmontop", "", evolveCandy25},
		// Linear item-gated (R7.P2).
		{"sunflora", testItemSunStone, evolveCandy50},
		{"kingdra", "dragon_scale", evolveCandy100},
		{"scizor", "metal_coat", evolveCandy50},
		{"steelix", "metal_coat", evolveCandy50},
		{"porygon2", "up_grade", evolveCandy25},
		{"porygon_z", "sinnoh_stone", evolveCandy100},
		{"rhyperior", "sinnoh_stone", evolveCandy100},
		{"electivire", "sinnoh_stone", evolveCandy100},
		{"magmortar", "sinnoh_stone", evolveCandy100},
		{"gliscor", "sinnoh_stone", evolveCandy100},
		{"dusknoir", "sinnoh_stone", evolveCandy100},
		{"togekiss", "sinnoh_stone", evolveCandy100},
		{"magnezone", "magnetic_lure", evolveCandy100},
		{"probopass", "magnetic_lure", evolveCandy50},
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
