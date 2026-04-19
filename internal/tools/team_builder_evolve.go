package tools

import (
	pogopvp "github.com/lexfrei/pogo-pvp-engine"
)

// autoEvolveMaxDepth caps the forward walk along Species.Evolutions
// so a malformed gamemaster cycle can't spin the helper forever.
// Real Pokémon GO chains top out at two hops (base → mid → final);
// five is a generous ceiling matching maxEvolutionDepth used by
// pvp_evolution_preview.
const autoEvolveMaxDepth = 5

// autoEvolvePool walks every pool member up its evolution chain to
// the deepest descendant that still fits the league CP cap at
// level 1. Mutates params.Pool in place: when a member is promoted,
// its Species / FastMove / ChargedMoves are overwritten (the
// rankings moveset lookup re-runs later against the new species) and
// its autoEvolvedFrom runtime bookkeeping captures the original id
// so computeMemberCost can surface the swap as a flag.
//
// Three terminal conditions distinct from full-terminal promotion:
//
//   - Branching chain: len(Evolutions) > 1 without a caller-
//     supplied target means the helper has no way to pick a
//     descendant (eevee → vaporeon / jolteon / flareon). Leave
//     the base form. Flag: "auto_evolve_skipped_branching:<orig>".
//   - First-hop over-cap: the immediate next evolution already
//     busts the league cap at level 1; no intermediate fit exists.
//     Leave the base form. Flag: "auto_evolve_over_cap:<orig>".
//   - Partial walk, terminal over-cap: some intermediate step
//     fits, but the chain's terminal form busts the cap. The
//     helper stops at the last fitting step and treats this as a
//     successful promotion (the member IS evolved — just not to
//     the absolute terminal). Flag: "auto_evolved_from:<orig>"
//     (same as full-terminal promotion). No separate
//     "terminal_over_cap" flag emitted; the caller can compare
//     the member's resolved Species against the original to spot
//     a partial walk.
//   - Unknown species / no snapshot: defensive no-op; the pool
//     validation phase already rejects unknowns with a hard error.
func autoEvolvePool(snapshot *pogopvp.Gamemaster, pool []Combatant, cpCap int) {
	if snapshot == nil {
		return
	}

	for i := range pool {
		autoEvolveMember(snapshot, &pool[i], cpCap)
	}
}

// autoEvolveMember walks the evolution chain for one pool member.
// Factored out of the per-pool loop so the branching / skip logic
// is independently testable.
func autoEvolveMember(snapshot *pogopvp.Gamemaster, spec *Combatant, cpCap int) {
	species, ok := snapshot.Pokemon[spec.Species]
	if !ok {
		return
	}

	if len(species.Evolutions) == 0 {
		return
	}

	originalID := spec.Species

	terminal, reason := walkEvolutionChain(snapshot, &species, spec.IV, cpCap)
	if reason != "" {
		spec.autoEvolvedFrom = originalID
		spec.autoEvolveSkip = reason

		return
	}

	if terminal == nil || terminal.ID == originalID {
		return
	}

	spec.Species = terminal.ID
	spec.autoEvolvedFrom = originalID
	// Clear moveset so defaultPoolMovesets re-queries rankings for
	// the evolved species (base-species recommended moveset is not
	// valid on the descendant; e.g. Gloom's VINE_WHIP is not in
	// Vileplume's learnable list).
	spec.FastMove = ""
	spec.ChargedMoves = nil
}

// walkEvolutionChain follows Species.Evolutions forward until one of:
//   - Terminal species reached (no further evolutions). Return it if
//     it fits the cap, else return (nil, "auto_evolve_over_cap").
//   - Branching species reached (len(Evolutions) > 1). Return
//     (nil, "auto_evolve_skipped_branching").
//   - Depth cap hit. Return whatever was last known to fit.
//
// The species at each step is validated against the CP cap using a
// level-1 CPM so the "fits" check is the floor CP of the form —
// matches validatePoolForLeague's semantics.
//
//nolint:gocritic // unnamedResult: (terminal species, skip reason) documented in godoc
func walkEvolutionChain(
	snapshot *pogopvp.Gamemaster, base *pogopvp.Species, ivs [3]int, cpCap int,
) (*pogopvp.Species, string) {
	current := base

	var lastFit *pogopvp.Species

	ivSpread, err := pogopvp.NewIV(ivs[0], ivs[1], ivs[2])
	if err != nil {
		return nil, ""
	}

	cpm, err := pogopvp.CPMAt(pogopvp.MinLevel)
	if err != nil {
		return nil, ""
	}

	for range autoEvolveMaxDepth {
		if len(current.Evolutions) == 0 {
			break
		}

		if len(current.Evolutions) > 1 {
			return nil, "auto_evolve_skipped_branching"
		}

		nextID := current.Evolutions[0]

		next, ok := snapshot.Pokemon[nextID]
		if !ok {
			break
		}

		if pogopvp.ComputeCP(next.BaseStats, ivSpread, cpm) > cpCap {
			if lastFit != nil {
				return lastFit, ""
			}

			return nil, "auto_evolve_over_cap"
		}

		lastFit = &next
		current = &next
	}

	if lastFit != nil {
		return lastFit, ""
	}

	return nil, ""
}

// autoEvolveFlagsFor returns the per-member breakdown flags the
// auto-evolve pass would set on the spec. Shared with
// computeMemberCost so cost breakdowns surface the evolution
// provenance without duplicating the string-literal list.
func autoEvolveFlagsFor(spec *Combatant) []string {
	if spec.autoEvolvedFrom == "" {
		return nil
	}

	if spec.autoEvolveSkip != "" {
		return []string{spec.autoEvolveSkip + ":" + spec.autoEvolvedFrom}
	}

	return []string{"auto_evolved_from:" + spec.autoEvolvedFrom}
}
