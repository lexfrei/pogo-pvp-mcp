package tools

import "testing"

// The evolution-items tests live in the `tools` package (not
// `tools_test`) so they can reach the package-private helpers
// directly; this table is an internal implementation detail and
// there is no user-facing API to exercise it from outside.

const testItemSunStone = "sun_stone"

// TestEvolutionRequirementFor_BellossomNeedsSunStone pins the
// Sun-Stone branch Bulbapedia documents for gloom → bellossom.
// Cost table snapshot 2026-04; adjust if Niantic changes tiers.
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

// TestEvolutionRequirementFor_VileplumeNoItem confirms the sibling
// branch is item-free so clients can distinguish the two paths
// without assuming the item field is always populated.
func TestEvolutionRequirementFor_VileplumeNoItem(t *testing.T) {
	t.Parallel()

	req := evolutionRequirementFor("vileplume")
	if req == nil {
		t.Fatal("evolutionRequirementFor(vileplume) = nil, want populated")
	}
	if req.Item != "" {
		t.Errorf("Item = %q, want empty", req.Item)
	}
	if req.Candy != evolveCandy100 {
		t.Errorf("Candy = %d, want %d", req.Candy, evolveCandy100)
	}
}

// TestEvolutionRequirementFor_UnknownReturnsNil pins the fall-
// through for species outside the curated branching table — the
// caller must fall back to its own data source rather than assume
// a default. Linear evolutions (ivysaur → venusaur) land here by
// design, and species not shipped in GO (e.g. Scyther → Kleavor
// never ships as an in-game evolution) also land here.
func TestEvolutionRequirementFor_UnknownReturnsNil(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"ivysaur", "venusaur", "ditto", "kleavor", "completely-bogus-species"} {
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

// TestEvolutionRequirementFor_ClamperlRandomPick pins the
// Pokémon-GO-specific behaviour that diverges from mainline: both
// huntail and gorebyss are pure RNG (no item) for 50 candy each.
// Mainline's Deep Sea Tooth / Deep Sea Scale items do not exist in
// GO.
func TestEvolutionRequirementFor_ClamperlRandomPick(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"huntail", "gorebyss"} {
		req := evolutionRequirementFor(id)
		if req == nil {
			t.Fatalf("evolutionRequirementFor(%q) = nil, want populated (clamperl split is in-table)", id)
		}
		if req.Item != "" {
			t.Errorf("%s Item = %q, want empty (pvpoke-GO: no item, random pick)", id, req.Item)
		}
		if req.Candy != evolveCandy50 {
			t.Errorf("%s Candy = %d, want %d", id, req.Candy, evolveCandy50)
		}
		if req.Notes == "" {
			t.Errorf("%s Notes empty; want the random-pick caveat", id)
		}
	}
}

// TestEvolutionRequirementFor_EspeonUmbreonCandy pins the 25-candy
// contract for the buddy-km-gated eeveelutions. The 10 km walk +
// time-of-day mechanic is the gate, but the candy is still charged
// on top — zero would mean "no candy cost", which is wrong.
func TestEvolutionRequirementFor_EspeonUmbreonCandy(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"espeon", "umbreon"} {
		req := evolutionRequirementFor(id)
		if req == nil {
			t.Fatalf("evolutionRequirementFor(%q) = nil, want populated", id)
		}
		if req.Candy != evolveCandy25 {
			t.Errorf("%s Candy = %d, want %d (buddy-km mechanic is the gate, not a candy discount)",
				id, req.Candy, evolveCandy25)
		}
	}
}

// TestEvolutionRequirementFor_MagnetonAndNosepass pins the
// Magnetic-Lure gate for magnezone + probopass. Pre-review-round-2
// the table mislabelled magnezone as sinnoh_stone and probopass as
// mossy_lure (Leafeon's item). Both are in fact Magnetic Lure
// evolutions (Niantic docs, Bulbapedia 2026-04 snapshot).
func TestEvolutionRequirementFor_MagnetonAndNosepass(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"magnezone", "probopass"} {
		req := evolutionRequirementFor(id)
		if req == nil {
			t.Fatalf("evolutionRequirementFor(%q) = nil, want populated", id)
		}
		if req.Item != "magnetic_lure" {
			t.Errorf("%s Item = %q, want magnetic_lure", id, req.Item)
		}
	}
}
