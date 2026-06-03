package gamemaster_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
)

// Test fixture literals hoisted to constants so goconst stays quiet —
// these ids and type names repeat across multiple diff assertions.
const (
	testSpeciesMedicham  = "medicham"
	testSpeciesAzumarill = "azumarill"
	testSpeciesWhiscash  = "whiscash"

	testTypeFighting = "fighting"
	testTypePsychic  = "psychic"
	testTypeWater    = "water"
	testTypeGround   = "ground"

	testMoveCounter  = "COUNTER"
	testMoveIcePunch = "ICE_PUNCH"
	testMoveMudShot  = "MUD_SHOT"
)

// smallFixture is the shared base gamemaster for differ tests. Every
// scenario mutates a copy of this value and diffs it against the
// original.
func smallFixture() *pogopvp.Gamemaster {
	return &pogopvp.Gamemaster{
		Version: "2026-04-19",
		Pokemon: map[string]pogopvp.Species{
			testSpeciesMedicham: {
				ID: testSpeciesMedicham, Dex: 308, Name: "Medicham",
				BaseStats:    pogopvp.BaseStats{Atk: 121, Def: 152, HP: 155},
				Types:        []string{testTypeFighting, testTypePsychic},
				FastMoves:    []string{testMoveCounter, "PSYCHO_CUT"},
				ChargedMoves: []string{testMoveIcePunch, "PSYCHIC"},
			},
			testSpeciesAzumarill: {
				ID: testSpeciesAzumarill, Dex: 184, Name: "Azumarill",
				BaseStats:    pogopvp.BaseStats{Atk: 112, Def: 152, HP: 225},
				Types:        []string{testTypeWater, "fairy"},
				FastMoves:    []string{"BUBBLE"},
				ChargedMoves: []string{"ICE_BEAM", "PLAY_ROUGH"},
			},
		},
		Moves: map[string]pogopvp.Move{
			testMoveCounter: {
				ID: testMoveCounter, Type: testTypeFighting,
				Power: 8, Energy: 0, EnergyGain: 7, Cooldown: 1000, Turns: 2,
			},
			testMoveIcePunch: {
				ID: testMoveIcePunch, Type: "ice",
				Power: 55, Energy: 40, Cooldown: 500,
			},
		},
	}
}

func TestDiffGamemasters_Empty(t *testing.T) {
	t.Parallel()

	before := smallFixture()
	after := smallFixture()

	diff := gamemaster.DiffGamemasters(before, after)
	if !diff.Empty() {
		t.Errorf("Empty = false, want true on identical snapshots; diff = %+v", diff)
	}
}

func TestDiffGamemasters_AddsRemovesChanges(t *testing.T) {
	t.Parallel()

	before := smallFixture()
	after := smallFixture()

	// Species: add "whiscash", remove "azumarill", change medicham's
	// charged moves.
	after.Pokemon[testSpeciesWhiscash] = pogopvp.Species{
		ID: testSpeciesWhiscash, Dex: 340, Name: "Whiscash",
		BaseStats:    pogopvp.BaseStats{Atk: 151, Def: 142, HP: 188},
		Types:        []string{testTypeWater, testTypeGround},
		FastMoves:    []string{testMoveMudShot},
		ChargedMoves: []string{"BLIZZARD"},
	}
	delete(after.Pokemon, testSpeciesAzumarill)

	changed := after.Pokemon[testSpeciesMedicham]
	changed.ChargedMoves = []string{testMoveIcePunch, "DYNAMIC_PUNCH"}
	after.Pokemon[testSpeciesMedicham] = changed

	// Moves: add MUD_SHOT, remove ICE_PUNCH, change COUNTER's power.
	after.Moves[testMoveMudShot] = pogopvp.Move{
		ID: testMoveMudShot, Type: testTypeGround,
		Power: 3, Energy: 0, EnergyGain: 9, Cooldown: 1000, Turns: 2,
	}
	delete(after.Moves, testMoveIcePunch)

	counter := after.Moves[testMoveCounter]
	counter.Power = 10
	after.Moves[testMoveCounter] = counter

	diff := gamemaster.DiffGamemasters(before, after)

	if diff.Empty() {
		t.Fatal("Empty = true, want false")
	}

	wantAddedSpecies := []string{"whiscash"}
	if !equalSortedStrings(diff.AddedSpecies, wantAddedSpecies) {
		t.Errorf("AddedSpecies = %v, want %v", diff.AddedSpecies, wantAddedSpecies)
	}

	wantRemovedSpecies := []string{"azumarill"}
	if !equalSortedStrings(diff.RemovedSpecies, wantRemovedSpecies) {
		t.Errorf("RemovedSpecies = %v, want %v", diff.RemovedSpecies, wantRemovedSpecies)
	}

	if len(diff.ChangedSpecies) != 1 || diff.ChangedSpecies[0].ID != "medicham" {
		t.Errorf("ChangedSpecies = %+v, want exactly medicham", diff.ChangedSpecies)
	}

	wantAddedMoves := []string{"MUD_SHOT"}
	if !equalSortedStrings(diff.AddedMoves, wantAddedMoves) {
		t.Errorf("AddedMoves = %v, want %v", diff.AddedMoves, wantAddedMoves)
	}

	wantRemovedMoves := []string{"ICE_PUNCH"}
	if !equalSortedStrings(diff.RemovedMoves, wantRemovedMoves) {
		t.Errorf("RemovedMoves = %v, want %v", diff.RemovedMoves, wantRemovedMoves)
	}

	if len(diff.ChangedMoves) != 1 || diff.ChangedMoves[0].ID != "COUNTER" {
		t.Errorf("ChangedMoves = %+v, want exactly COUNTER", diff.ChangedMoves)
	}
	if diff.ChangedMoves[0].Before.Power != 8 || diff.ChangedMoves[0].After.Power != 10 {
		t.Errorf("COUNTER power diff = %d -> %d, want 8 -> 10",
			diff.ChangedMoves[0].Before.Power, diff.ChangedMoves[0].After.Power)
	}
}

// TestDiffGamemasters_TypesChangeDetected pins the Phase-review
// fix: a species whose only difference is its Types list must be
// surfaced as changed. Before the fix, a secondary-type flip would
// slip past the differ entirely and silently shift STAB / type
// effectiveness resolution downstream.
func TestDiffGamemasters_TypesChangeDetected(t *testing.T) {
	t.Parallel()

	before := smallFixture()
	after := smallFixture()

	// Clone medicham and flip its secondary type fighting -> dark.
	medicham := after.Pokemon[testSpeciesMedicham]
	medicham.Types = []string{"dark", testTypePsychic}
	after.Pokemon[testSpeciesMedicham] = medicham

	diff := gamemaster.DiffGamemasters(before, after)

	if diff.Empty() {
		t.Fatal("Empty = true, want false when Types differ")
	}

	if len(diff.ChangedSpecies) != 1 || diff.ChangedSpecies[0].ID != testSpeciesMedicham {
		t.Fatalf("ChangedSpecies = %+v, want exactly %s",
			diff.ChangedSpecies, testSpeciesMedicham)
	}

	entry := diff.ChangedSpecies[0]

	if !slices.Contains(entry.TypesBefore, "fighting") {
		t.Errorf("TypesBefore = %v, want to contain fighting", entry.TypesBefore)
	}
	if !slices.Contains(entry.TypesAfter, "dark") {
		t.Errorf("TypesAfter = %v, want to contain dark", entry.TypesAfter)
	}
}

// TestDiffGamemasters_NilBaseline verifies that a nil "before" is
// treated as an empty baseline — every entry in "after" shows up as
// an addition. Useful when bootstrapping a diff on a fresh install
// with no cache yet.
func TestDiffGamemasters_NilBaseline(t *testing.T) {
	t.Parallel()

	after := smallFixture()

	diff := gamemaster.DiffGamemasters(nil, after)

	if len(diff.AddedSpecies) != 2 {
		t.Errorf("AddedSpecies len = %d, want 2", len(diff.AddedSpecies))
	}
	if len(diff.RemovedSpecies) != 0 {
		t.Errorf("RemovedSpecies len = %d, want 0", len(diff.RemovedSpecies))
	}
	if len(diff.AddedMoves) != 2 {
		t.Errorf("AddedMoves len = %d, want 2", len(diff.AddedMoves))
	}
}

// TestWriteDiff_NoChanges verifies the "no changes" short-circuit.
func TestWriteDiff_NoChanges(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	diff := gamemaster.Diff{}
	gamemaster.WriteDiff(&buf, &diff)

	if !strings.Contains(buf.String(), "no changes") {
		t.Errorf("empty diff output = %q, want \"no changes\" line", buf.String())
	}
}

// TestWriteDiff_ListsAdditions verifies each section header shows up
// when it has entries and stays hidden otherwise.
func TestWriteDiff_ListsAdditions(t *testing.T) {
	t.Parallel()

	diff := gamemaster.Diff{
		AddedSpecies: []string{"x", "y"},
		AddedMoves:   []string{"M1"},
	}

	var buf bytes.Buffer
	gamemaster.WriteDiff(&buf, &diff)

	output := buf.String()

	if !strings.Contains(output, "added species (2):") {
		t.Errorf("missing added-species header; output = %q", output)
	}
	if !strings.Contains(output, "added moves (1):") {
		t.Errorf("missing added-moves header; output = %q", output)
	}
	if strings.Contains(output, "removed species") {
		t.Errorf("unexpected removed-species header in empty diff")
	}
}

// equalSortedStrings compares two already-sorted string slices.
func equalSortedStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
