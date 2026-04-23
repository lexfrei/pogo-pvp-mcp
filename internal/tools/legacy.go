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

// ErrLegacyConflict is returned by the team tools when a Combatant
// carries an explicit moveset whose ids include a pvpoke-legacy
// (permanently-removed) move while DisallowLegacy=true. Hard failure
// before any simulation so the client learns about the incompatibility
// instead of getting a subtly-weaker result.
var ErrLegacyConflict = errors.New("legacy move present but disallow_legacy=true")

// ErrEliteConflict is the sibling of ErrLegacyConflict for the elite
// category — moves locked behind Elite TM, Community Day events, or
// limited-time research (Venusaur FRENZY_PLANT, Quagsire AQUA_TAIL).
// Surfaced when a Combatant carries an elite move under
// DisallowElite=true.
var ErrEliteConflict = errors.New(
	"elite move (Elite TM / Community Day / event) present but disallow_elite=true")

// MoveRef is the per-move JSON projection that pvp_meta, pvp_rank,
// and pvp_species_info use when they need to surface the restricted
// flags alongside the id. Legacy and Elite are independent, disjoint
// pvpoke categories:
//   - Legacy = permanently removed from the learn-pool (Grimer ACID).
//     The player either already has one or will never get one.
//   - Elite = gated behind Elite TM or Community Day (Venusaur
//     FRENZY_PLANT, Quagsire AQUA_TAIL). Obtainable via a specific
//     path, not via regular TMs.
//
// Both flags are scoped to the enclosing species — the same id can
// be legacy on one species, elite on another, or regular on a third.
type MoveRef struct {
	ID     string `json:"id"`
	Legacy bool   `json:"legacy"`
	Elite  bool   `json:"elite"`
}

// newMoveRef tags one move id with its legacy and elite flags for
// the given species. A nil species returns both flags false
// defensively so callers can feed in arbitrary ids without special
// cases.
func newMoveRef(species *pogopvp.Species, moveID string) MoveRef {
	return MoveRef{
		ID:     moveID,
		Legacy: pogopvp.IsLegacyMove(species, moveID),
		Elite:  pogopvp.IsEliteMove(species, moveID),
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
	return filterMovesByCategory(species, ids, pogopvp.IsLegacyMove)
}

// nonEliteMoves partitions the species' full move list into the
// non-elite subset. Mirror of nonLegacyMoves for the elite
// category.
func nonEliteMoves(species *pogopvp.Species, ids []string) []string {
	return filterMovesByCategory(species, ids, pogopvp.IsEliteMove)
}

// filterMovesByCategory returns the subset of ids that do NOT
// satisfy the predicate (i.e. strip moves that belong to the
// given restricted category). Preserves input order.
func filterMovesByCategory(
	species *pogopvp.Species, ids []string,
	predicate func(*pogopvp.Species, string) bool,
) []string {
	if species == nil {
		return ids
	}

	out := make([]string, 0, len(ids))

	for _, id := range ids {
		if predicate(species, id) {
			continue
		}

		out = append(out, id)
	}

	return out
}

// anyEliteMove reports whether any id in moveset is elite on the
// given species. Mirror of anyLegacyMove for the elite category.
func anyEliteMove(species *pogopvp.Species, moveset []string) bool {
	for _, id := range moveset {
		if pogopvp.IsEliteMove(species, id) {
			return true
		}
	}

	return false
}

// movesetInRestrictedCategory reports whether any id in moveset is
// in the restricted category on the given species. Used by the
// meta-fallback filter in pvp_counter_finder so one loop covers
// both the legacy and elite filters via the category predicate.
func movesetInRestrictedCategory(
	species *pogopvp.Species, moveset []string, cat restrictedCategory,
) bool {
	for _, id := range moveset {
		if cat.predicate(species, id) {
			return true
		}
	}

	return false
}

// restrictedCategory bundles a restricted-move predicate (legacy or
// elite) with the sentinel error surfaced on hit and a short label
// used in error messages. Avoids source-level duplication between
// the legacy- and elite-guard paths.
type restrictedCategory struct {
	// predicate tests whether a move id is in the category on the
	// given species. Safe against a nil species.
	predicate func(*pogopvp.Species, string) bool
	sentinel  error
	label     string
}

//nolint:gochecknoglobals // small fixed set of pvpoke restriction categories
var (
	legacyCategory = restrictedCategory{
		predicate: pogopvp.IsLegacyMove,
		sentinel:  ErrLegacyConflict,
		label:     "legacy",
	}
	eliteCategory = restrictedCategory{
		predicate: pogopvp.IsEliteMove,
		sentinel:  ErrEliteConflict,
		label:     "elite",
	}
)

// assertNoRestrictedInCombatantCategory inspects a Combatant's
// explicit moveset under `disallow=true` against one restricted
// category (legacy or elite). See assertNoLegacyInCombatant /
// assertNoEliteInCombatant for the per-category wrappers that
// callers actually invoke.
//
// snapshot is the active gamemaster. A nil or species-missing
// snapshot returns nil (no conflict detected) — downstream code
// will surface the usual ErrUnknownSpecies via the normal combatant
// builder path.
func assertNoRestrictedInCombatantCategory(
	snapshot *pogopvp.Gamemaster, spec *Combatant, disallow bool, cat restrictedCategory,
) error {
	if !disallow || snapshot == nil {
		return nil
	}

	species, _, _, ok := resolveSpeciesLookup(snapshot, spec.Species, spec.Options)
	if !ok {
		return nil
	}

	if spec.FastMove != "" && cat.predicate(&species, spec.FastMove) {
		return fmt.Errorf("%w: species=%q fast_move=%q",
			cat.sentinel, spec.Species, spec.FastMove)
	}

	for _, id := range spec.ChargedMoves {
		if cat.predicate(&species, id) {
			return fmt.Errorf("%w: species=%q charged_move=%q",
				cat.sentinel, spec.Species, id)
		}
	}

	return nil
}

// assertNoLegacyInCombatant guards a Combatant against pvpoke
// legacyMoves (permanently-removed). Empty FastMove / ChargedMoves
// skip the check — the caller is expected to call
// applyMovesetDefaults which routes through rejectResolvedLegacy
// for the auto-fill case.
func assertNoLegacyInCombatant(
	snapshot *pogopvp.Gamemaster, spec *Combatant, disallowLegacy bool,
) error {
	return assertNoRestrictedInCombatantCategory(snapshot, spec, disallowLegacy, legacyCategory)
}

// assertNoEliteInCombatant guards a Combatant against pvpoke
// eliteMoves (Elite TM / Community Day).
func assertNoEliteInCombatant(
	snapshot *pogopvp.Gamemaster, spec *Combatant, disallowElite bool,
) error {
	return assertNoRestrictedInCombatantCategory(snapshot, spec, disallowElite, eliteCategory)
}

// assertNoRestrictedInCombatant applies both category gates in one
// call. Each flag is independent: both off = no check, legacy only =
// legacy-category rejection, elite only = elite-category rejection,
// both on = either category triggers.
func assertNoRestrictedInCombatant(
	snapshot *pogopvp.Gamemaster, spec *Combatant, disallowLegacy, disallowElite bool,
) error {
	err := assertNoLegacyInCombatant(snapshot, spec, disallowLegacy)
	if err != nil {
		return err
	}

	return assertNoEliteInCombatant(snapshot, spec, disallowElite)
}

// rejectResolvedCategory guards the output of ResolveMoveset
// against one restricted category. pvpoke's recommended moveset can
// itself contain legacy or elite moves; without this guard the team
// tools would silently auto-fill a combatant with a move the caller
// explicitly rejected.
func rejectResolvedCategory(
	snapshot *pogopvp.Gamemaster,
	speciesID, fast string, charged []string, disallow bool, cat restrictedCategory,
) error {
	if !disallow || snapshot == nil {
		return nil
	}

	species, ok := snapshot.Pokemon[speciesID]
	if !ok {
		return nil
	}

	if cat.predicate(&species, fast) {
		return fmt.Errorf("%w: species=%q recommended fast=%q is %s",
			cat.sentinel, speciesID, fast, cat.label)
	}

	for _, id := range charged {
		if cat.predicate(&species, id) {
			return fmt.Errorf("%w: species=%q recommended charged=%q is %s",
				cat.sentinel, speciesID, id, cat.label)
		}
	}

	return nil
}

// rejectResolvedLegacy is the legacy-category wrapper over
// rejectResolvedCategory. Preserved as the public name used by the
// team tools.
func rejectResolvedLegacy(
	snapshot *pogopvp.Gamemaster,
	speciesID, fast string, charged []string, disallowLegacy bool,
) error {
	return rejectResolvedCategory(snapshot, speciesID, fast, charged, disallowLegacy, legacyCategory)
}

// rejectResolvedElite is the elite-category wrapper.
func rejectResolvedElite(
	snapshot *pogopvp.Gamemaster,
	speciesID, fast string, charged []string, disallowElite bool,
) error {
	return rejectResolvedCategory(snapshot, speciesID, fast, charged, disallowElite, eliteCategory)
}

// rejectResolvedRestricted runs both legacy and elite post-resolve
// gates under the two independent flags.
func rejectResolvedRestricted(
	snapshot *pogopvp.Gamemaster,
	speciesID, fast string, charged []string, disallowLegacy, disallowElite bool,
) error {
	err := rejectResolvedLegacy(snapshot, speciesID, fast, charged, disallowLegacy)
	if err != nil {
		return err
	}

	return rejectResolvedElite(snapshot, speciesID, fast, charged, disallowElite)
}

// rejectTeamRestricted walks a Combatant slice under the two
// independent restriction flags. Returns the first conflict (either
// category) with a member-index prefix. Both flags off → fast-exit
// without touching the snapshot (common path; test-time perf matters).
func rejectTeamRestricted(
	snapshot *pogopvp.Gamemaster, team []Combatant, disallowLegacy, disallowElite bool,
) error {
	if !disallowLegacy && !disallowElite {
		return nil
	}

	for i := range team {
		err := assertNoRestrictedInCombatant(snapshot, &team[i], disallowLegacy, disallowElite)
		if err != nil {
			return fmt.Errorf("team[%d]: %w", i, err)
		}
	}

	return nil
}
