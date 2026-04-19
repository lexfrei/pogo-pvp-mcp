package gamemaster

import (
	"fmt"
	"io"
	"slices"
	"sort"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
)

// Diff describes the delta between two gamemaster snapshots. Added
// and Removed slices carry the IDs; Changed slices carry before/after
// projections of the fields the differ considers authoritative for
// balance: base stats + legal move list for species; power / energy
// / cooldown / turns / type / energy gain for moves.
type Diff struct {
	AddedSpecies   []string
	RemovedSpecies []string
	ChangedSpecies []ChangedSpecies
	AddedMoves     []string
	RemovedMoves   []string
	ChangedMoves   []ChangedMove
}

// ChangedSpecies captures the before/after projection of a single
// species. Only the fields that affect PvP math are compared —
// cosmetic fields (Tags, Dex, Released) are ignored. Types matter
// because STAB and type-effectiveness resolution are driven off
// Species.Types; a silent Types-only change would flip matchup
// outcomes while the differ reported "no changes".
type ChangedSpecies struct {
	ID                 string
	BaseStatsBefore    pogopvp.BaseStats
	BaseStatsAfter     pogopvp.BaseStats
	TypesBefore        []string
	TypesAfter         []string
	FastMovesBefore    []string
	FastMovesAfter     []string
	ChargedMovesBefore []string
	ChargedMovesAfter  []string
}

// ChangedMove captures the before/after projection of a single move.
type ChangedMove struct {
	ID     string
	Before pogopvp.Move
	After  pogopvp.Move
}

// Empty reports whether the diff carries any changes at all. Used by
// the CLI command to pick an exit code and the human report's
// "gamemaster is in sync" line.
func (diff *Diff) Empty() bool {
	return len(diff.AddedSpecies) == 0 &&
		len(diff.RemovedSpecies) == 0 &&
		len(diff.ChangedSpecies) == 0 &&
		len(diff.AddedMoves) == 0 &&
		len(diff.RemovedMoves) == 0 &&
		len(diff.ChangedMoves) == 0
}

// DiffGamemasters computes the structural delta between two parsed
// gamemaster snapshots. The result is deterministic: all slice
// fields are sorted by ID so the output stream is stable across
// process invocations. A nil input is treated as "no entries" —
// handy for bootstrapping diffs against an empty baseline.
func DiffGamemasters(before, after *pogopvp.Gamemaster) Diff {
	beforeSpecies := speciesMap(before)
	afterSpecies := speciesMap(after)
	beforeMoves := movesMap(before)
	afterMoves := movesMap(after)

	out := Diff{}
	diffSpeciesInto(&out, beforeSpecies, afterSpecies)
	diffMovesInto(&out, beforeMoves, afterMoves)

	return out
}

// speciesMap returns the Species map from a (possibly nil) snapshot.
func speciesMap(gm *pogopvp.Gamemaster) map[string]pogopvp.Species {
	if gm == nil {
		return nil
	}

	return gm.Pokemon
}

// movesMap returns the Moves map from a (possibly nil) snapshot.
func movesMap(gm *pogopvp.Gamemaster) map[string]pogopvp.Move {
	if gm == nil {
		return nil
	}

	return gm.Moves
}

// diffSpeciesInto populates the species section of diff from the two
// snapshots. Emits adds, removes, and per-species change records
// sorted by species id so the downstream report stays stable.
func diffSpeciesInto(
	diff *Diff, before, after map[string]pogopvp.Species,
) {
	for speciesID := range after {
		_, ok := before[speciesID]
		if !ok {
			diff.AddedSpecies = append(diff.AddedSpecies, speciesID)
		}
	}

	for speciesID := range before {
		current, ok := after[speciesID]
		if !ok {
			diff.RemovedSpecies = append(diff.RemovedSpecies, speciesID)

			continue
		}

		old := before[speciesID]
		if speciesChanged(&old, &current) {
			diff.ChangedSpecies = append(diff.ChangedSpecies, ChangedSpecies{
				ID:                 speciesID,
				BaseStatsBefore:    old.BaseStats,
				BaseStatsAfter:     current.BaseStats,
				TypesBefore:        old.Types,
				TypesAfter:         current.Types,
				FastMovesBefore:    old.FastMoves,
				FastMovesAfter:     current.FastMoves,
				ChargedMovesBefore: old.ChargedMoves,
				ChargedMovesAfter:  current.ChargedMoves,
			})
		}
	}

	sort.Strings(diff.AddedSpecies)
	sort.Strings(diff.RemovedSpecies)
	sort.Slice(diff.ChangedSpecies, func(i, j int) bool {
		return diff.ChangedSpecies[i].ID < diff.ChangedSpecies[j].ID
	})
}

// speciesChanged reports whether two species entries differ in any
// PvP-relevant field: base stats, type list (order-insensitive —
// pvpoke's primary/secondary ordering is not semantically
// meaningful to the engine), or legal-move list.
func speciesChanged(before, after *pogopvp.Species) bool {
	if before.BaseStats != after.BaseStats {
		return true
	}

	if !stringSetEqual(before.Types, after.Types) {
		return true
	}

	if !stringSetEqual(before.FastMoves, after.FastMoves) {
		return true
	}

	if !stringSetEqual(before.ChargedMoves, after.ChargedMoves) {
		return true
	}

	return false
}

// stringSetEqual compares two string slices ignoring order. Good
// enough for pvpoke move lists where upstream never emits duplicates.
func stringSetEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	sortedA := append([]string(nil), a...)
	sortedB := append([]string(nil), b...)

	slices.Sort(sortedA)
	slices.Sort(sortedB)

	return slices.Equal(sortedA, sortedB)
}

// diffMovesInto mirrors diffSpeciesInto for the Moves map.
func diffMovesInto(
	diff *Diff, before, after map[string]pogopvp.Move,
) {
	for moveID := range after {
		_, ok := before[moveID]
		if !ok {
			diff.AddedMoves = append(diff.AddedMoves, moveID)
		}
	}

	for moveID := range before {
		current, ok := after[moveID]
		if !ok {
			diff.RemovedMoves = append(diff.RemovedMoves, moveID)

			continue
		}

		old := before[moveID]
		if moveChanged(&old, &current) {
			diff.ChangedMoves = append(diff.ChangedMoves,
				ChangedMove{ID: moveID, Before: old, After: current})
		}
	}

	sort.Strings(diff.AddedMoves)
	sort.Strings(diff.RemovedMoves)
	sort.Slice(diff.ChangedMoves, func(i, j int) bool {
		return diff.ChangedMoves[i].ID < diff.ChangedMoves[j].ID
	})
}

// moveChanged reports whether two move entries differ in any
// PvP-relevant field. Category is inferred from energy gain / cost
// and cannot flip independently of the numeric fields that are
// already checked here.
func moveChanged(before, after *pogopvp.Move) bool {
	return before.Type != after.Type ||
		before.Power != after.Power ||
		before.Energy != after.Energy ||
		before.EnergyGain != after.EnergyGain ||
		before.Cooldown != after.Cooldown ||
		before.Turns != after.Turns
}

// WriteDiff prints a human-readable summary of the diff to w. Empty
// diffs produce a single "no changes" line so operators can tell a
// successful comparison from a silent failure.
func WriteDiff(out io.Writer, diff *Diff) {
	if diff.Empty() {
		fmt.Fprintln(out, "gamemaster: no changes")

		return
	}

	writeAddedSpecies(out, diff.AddedSpecies)
	writeRemovedSpecies(out, diff.RemovedSpecies)
	writeChangedSpecies(out, diff.ChangedSpecies)
	writeAddedMoves(out, diff.AddedMoves)
	writeRemovedMoves(out, diff.RemovedMoves)
	writeChangedMoves(out, diff.ChangedMoves)
}

// writeAddedSpecies writes the added-species block or nothing.
func writeAddedSpecies(out io.Writer, ids []string) {
	if len(ids) == 0 {
		return
	}

	fmt.Fprintf(out, "added species (%d):\n", len(ids))

	for _, id := range ids {
		fmt.Fprintf(out, "  + %s\n", id)
	}
}

// writeRemovedSpecies writes the removed-species block or nothing.
func writeRemovedSpecies(out io.Writer, ids []string) {
	if len(ids) == 0 {
		return
	}

	fmt.Fprintf(out, "removed species (%d):\n", len(ids))

	for _, id := range ids {
		fmt.Fprintf(out, "  - %s\n", id)
	}
}

// writeChangedSpecies writes the changed-species block or nothing.
func writeChangedSpecies(out io.Writer, entries []ChangedSpecies) {
	if len(entries) == 0 {
		return
	}

	fmt.Fprintf(out, "changed species (%d):\n", len(entries))

	for i := range entries {
		writeSpeciesEntry(out, &entries[i])
	}
}

// writeSpeciesEntry prints one changed-species record, skipping
// sub-sections that didn't actually change.
func writeSpeciesEntry(out io.Writer, entry *ChangedSpecies) {
	fmt.Fprintf(out, "  ~ %s\n", entry.ID)

	if entry.BaseStatsBefore != entry.BaseStatsAfter {
		fmt.Fprintf(out, "      stats: %+v -> %+v\n",
			entry.BaseStatsBefore, entry.BaseStatsAfter)
	}

	if !stringSetEqual(entry.TypesBefore, entry.TypesAfter) {
		fmt.Fprintf(out, "      types: %v -> %v\n",
			entry.TypesBefore, entry.TypesAfter)
	}

	if !stringSetEqual(entry.FastMovesBefore, entry.FastMovesAfter) {
		fmt.Fprintf(out, "      fast: %v -> %v\n",
			entry.FastMovesBefore, entry.FastMovesAfter)
	}

	if !stringSetEqual(entry.ChargedMovesBefore, entry.ChargedMovesAfter) {
		fmt.Fprintf(out, "      charged: %v -> %v\n",
			entry.ChargedMovesBefore, entry.ChargedMovesAfter)
	}
}

// writeAddedMoves writes the added-moves block or nothing.
func writeAddedMoves(out io.Writer, ids []string) {
	if len(ids) == 0 {
		return
	}

	fmt.Fprintf(out, "added moves (%d):\n", len(ids))

	for _, id := range ids {
		fmt.Fprintf(out, "  + %s\n", id)
	}
}

// writeRemovedMoves writes the removed-moves block or nothing.
func writeRemovedMoves(out io.Writer, ids []string) {
	if len(ids) == 0 {
		return
	}

	fmt.Fprintf(out, "removed moves (%d):\n", len(ids))

	for _, id := range ids {
		fmt.Fprintf(out, "  - %s\n", id)
	}
}

// writeChangedMoves writes the changed-moves block or nothing.
func writeChangedMoves(out io.Writer, entries []ChangedMove) {
	if len(entries) == 0 {
		return
	}

	fmt.Fprintf(out, "changed moves (%d):\n", len(entries))

	for i := range entries {
		writeMoveEntry(out, &entries[i])
	}
}

// writeMoveEntry prints one changed-move record on a single line so
// `diff-gm | grep MOVE_ID` returns a complete record.
func writeMoveEntry(out io.Writer, entry *ChangedMove) {
	fmt.Fprintf(out,
		"  ~ %s power=%d->%d energy=%d->%d gain=%d->%d cd=%d->%d turns=%d->%d type=%s->%s\n",
		entry.ID,
		entry.Before.Power, entry.After.Power,
		entry.Before.Energy, entry.After.Energy,
		entry.Before.EnergyGain, entry.After.EnergyGain,
		entry.Before.Cooldown, entry.After.Cooldown,
		entry.Before.Turns, entry.After.Turns,
		entry.Before.Type, entry.After.Type,
	)
}
