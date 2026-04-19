package tools

// legacy.go hosts the tool-facing types and helpers for per-species
// legacy-move flags. Engine already carries the source of truth
// (Species.LegacyMoves + pogopvp.IsLegacyMove); this file adapts
// that into the MCP wire shapes (MoveRef) and the resolver used by
// pvp_rank's non-legacy moveset search and the disallow_legacy flag
// on the team tools.

import (
	"errors"
	"fmt"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
)

// ErrLegacyConflict is returned by pvp_team_analysis /
// pvp_team_builder when a Combatant carries an explicit moveset
// whose ids include a legacy move while DisallowLegacy=true. Hard
// failure before any simulation so the client learns about the
// incompatibility instead of getting a subtly-weaker result.
var ErrLegacyConflict = errors.New("legacy move present but disallow_legacy=true")

// MoveRef is the per-move JSON projection that pvp_meta and
// pvp_rank use when they need to surface the legacy flag alongside
// the id. Legacy is scoped to the enclosing species; the same id
// can be legacy on one species and regular on another.
type MoveRef struct {
	ID     string `json:"id"`
	Legacy bool   `json:"legacy"`
}

// newMoveRef tags one move id with its legacy state for the given
// species. A nil species or an unknown species returns Legacy=false
// defensively so callers can feed in arbitrary ids without special
// cases.
func newMoveRef(species *pogopvp.Species, moveID string) MoveRef {
	return MoveRef{
		ID:     moveID,
		Legacy: pogopvp.IsLegacyMove(species, moveID),
	}
}

// moveRefsFrom builds a []MoveRef from a slice of move ids,
// delegating the per-move legacy check to newMoveRef. Pre-allocates
// a non-nil empty slice so JSON marshalling emits `[]` for empty
// input, not `null`.
func moveRefsFrom(species *pogopvp.Species, ids []string) []MoveRef {
	out := make([]MoveRef, 0, len(ids))
	for _, id := range ids {
		out = append(out, newMoveRef(species, id))
	}

	return out
}

// containsLegacyMove reports whether any id in moveset is a legacy
// move on the given species. Used by the team tools to reject
// explicit legacy input under allow_legacy=false.
func containsLegacyMove(species *pogopvp.Species, moveset []string) (string, bool) {
	for _, id := range moveset {
		if pogopvp.IsLegacyMove(species, id) {
			return id, true
		}
	}

	return "", false
}

// anyLegacyMove is the bool-only variant of containsLegacyMove for
// call sites that don't need the offending id.
func anyLegacyMove(species *pogopvp.Species, moveset []string) bool {
	_, found := containsLegacyMove(species, moveset)

	return found
}

// nonLegacyMoves partitions the species' full move list into the
// non-legacy subset. Preserves input order so the enumeration is
// deterministic across invocations.
func nonLegacyMoves(species *pogopvp.Species, ids []string) []string {
	if species == nil {
		return ids
	}

	out := make([]string, 0, len(ids))

	for _, id := range ids {
		if pogopvp.IsLegacyMove(species, id) {
			continue
		}

		out = append(out, id)
	}

	return out
}

// assertNoLegacyInCombatant inspects a Combatant's explicit moveset
// under disallow_legacy=true and returns ErrLegacyConflict with a
// descriptive message if any of its moves are legacy on the target
// species. Empty FastMove / ChargedMoves skip the check — the
// caller is expected to then call applyMovesetDefaults with the
// same disallowLegacy flag, which routes through rejectResolvedLegacy
// and enforces the gate on the auto-resolved moveset too.
//
// snapshot is the active gamemaster. A nil or species-missing
// snapshot returns nil (no conflict detected) — downstream code
// will surface the usual ErrUnknownSpecies via the normal combatant
// builder path.
func assertNoLegacyInCombatant(
	snapshot *pogopvp.Gamemaster, spec *Combatant, disallowLegacy bool,
) error {
	if !disallowLegacy || snapshot == nil {
		return nil
	}

	species, ok := snapshot.Pokemon[spec.Species]
	if !ok {
		return nil
	}

	if spec.FastMove != "" && pogopvp.IsLegacyMove(&species, spec.FastMove) {
		return fmt.Errorf("%w: species=%q fast_move=%q",
			ErrLegacyConflict, spec.Species, spec.FastMove)
	}

	if conflicting, found := containsLegacyMove(&species, spec.ChargedMoves); found {
		return fmt.Errorf("%w: species=%q charged_move=%q",
			ErrLegacyConflict, spec.Species, conflicting)
	}

	return nil
}

// rejectResolvedLegacy guards the output of ResolveMoveset against
// legacy moves when the caller has disallowLegacy=true. pvpoke's
// recommended moveset can itself contain legacy moves (community-day
// or event-exclusive picks); without this guard the team tools would
// silently auto-fill a combatant with a move the caller explicitly
// rejected. A nil snapshot or disallowLegacy=false fast-exits.
func rejectResolvedLegacy(
	snapshot *pogopvp.Gamemaster,
	speciesID, fast string, charged []string, disallowLegacy bool,
) error {
	if !disallowLegacy || snapshot == nil {
		return nil
	}

	species, ok := snapshot.Pokemon[speciesID]
	if !ok {
		return nil
	}

	if pogopvp.IsLegacyMove(&species, fast) {
		return fmt.Errorf("%w: species=%q recommended fast=%q is legacy",
			ErrLegacyConflict, speciesID, fast)
	}

	if conflict, found := containsLegacyMove(&species, charged); found {
		return fmt.Errorf("%w: species=%q recommended charged=%q is legacy",
			ErrLegacyConflict, speciesID, conflict)
	}

	return nil
}

// rejectTeamLegacy walks a Combatant slice under disallowLegacy=true
// and returns the first ErrLegacyConflict with a member-index
// prefix. A disallowLegacy=false fast-exits without touching the
// snapshot (common path; test-time perf matters).
func rejectTeamLegacy(
	snapshot *pogopvp.Gamemaster, team []Combatant, disallowLegacy bool,
) error {
	if !disallowLegacy {
		return nil
	}

	for i := range team {
		err := assertNoLegacyInCombatant(snapshot, &team[i], true)
		if err != nil {
			return fmt.Errorf("team[%d]: %w", i, err)
		}
	}

	return nil
}
