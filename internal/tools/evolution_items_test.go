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

	req, ok := evolutionRequirementFor("bellossom")
	if !ok {
		t.Fatal("evolutionRequirementFor(bellossom) = !ok, want ok")
	}
	if req.Item != testItemSunStone {
		t.Errorf("Item = %q, want sun_stone", req.Item)
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

	req, ok := evolutionRequirementFor("vileplume")
	if !ok {
		t.Fatal("evolutionRequirementFor(vileplume) = !ok, want ok")
	}
	if req.Item != "" {
		t.Errorf("Item = %q, want empty", req.Item)
	}
	if req.Candy != evolveCandy100 {
		t.Errorf("Candy = %d, want %d", req.Candy, evolveCandy100)
	}
}

// TestEvolutionRequirementFor_UnknownReturnsFalse pins the fall-
// through for species outside the curated branching table — the
// caller must fall back to its own data source rather than assume
// a default. Linear evolutions (ivysaur → venusaur) land here by
// design.
func TestEvolutionRequirementFor_UnknownReturnsFalse(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"ivysaur", "venusaur", "ditto", "completely-bogus-species"} {
		req, ok := evolutionRequirementFor(id)
		if ok {
			t.Errorf("evolutionRequirementFor(%q) = ok (req=%+v), want !ok", id, req)
		}
		if req != nil {
			t.Errorf("evolutionRequirementFor(%q) req = %+v, want nil", id, req)
		}
	}
}

// TestEvolutionRequirementFor_ReturnsCopy pins that the helper
// hands back an independent struct — caller mutations must not
// pollute the shared table. Verified by mutating the result and
// re-querying.
func TestEvolutionRequirementFor_ReturnsCopy(t *testing.T) {
	t.Parallel()

	first, ok := evolutionRequirementFor("bellossom")
	if !ok {
		t.Fatal("first lookup failed")
	}

	first.Item = "MUTATED"
	first.Candy = 9999

	second, ok := evolutionRequirementFor("bellossom")
	if !ok {
		t.Fatal("second lookup failed")
	}

	if second.Item != testItemSunStone || second.Candy != evolveCandy100 {
		t.Errorf("shared table mutated: second = %+v, want {sun_stone, %d}",
			second, evolveCandy100)
	}
}
