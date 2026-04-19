package tools

import (
	"context"
	"errors"
	"fmt"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
)

// chargedMoveIDs projects an engine Combatant's charged moves to a
// slice of string ids for the tool-level echo shapes. An empty input
// returns an empty (non-nil) slice so JSON marshalling emits `[]`
// rather than `null` — the echo field on the several result types
// (TeamMemberAnalysis, ResolvedCombatant) needs a consistent wire
// shape regardless of whether the engine Combatant was fast-only.
func chargedMoveIDs(moves []pogopvp.Move) []string {
	out := make([]string, len(moves))
	for i := range moves {
		out[i] = moves[i].ID
	}

	return out
}

// ErrNoRecommendedMoveset is returned by ResolveMoveset when the
// species is not present in the rankings slice for the requested
// (cup, cap) pair. Battle tools that defaulted a combatant's moves
// must surface this so the caller learns that the auto-fallback
// failed rather than simulating with an empty moveset.
var ErrNoRecommendedMoveset = errors.New("no recommended moveset for species in cup/cap rankings")

// ResolveMoveset looks up the pvpoke recommended moveset for the
// species under the requested (cup, cap) and splits the first slot
// out as the fast move with the remainder as charged moves.
// Backstops:
//   - ranks==nil → ErrNoRecommendedMoveset (caller constructed the
//     tool without a rankings manager and asked the tool to default a
//     moveset; this is a programming error visible to the client).
//   - rankings fetch fails → wrapped fetch error (retry-able).
//   - species missing from the ranking slice → ErrNoRecommendedMoveset.
//   - ranking entry carries an empty moveset → ErrNoRecommendedMoveset.
//
//nolint:gocritic // unnamedResult conflicts with the repo-wide nonamedreturns rule
func ResolveMoveset(
	ctx context.Context, ranks *rankings.Manager,
	species string, cpCap int, cup string,
) (string, []string, error) {
	if ranks == nil {
		return "", nil, fmt.Errorf("%w: %q", ErrNoRecommendedMoveset, species)
	}

	entries, err := ranks.Get(ctx, cpCap, cup)
	if err != nil {
		return "", nil, fmt.Errorf("rankings fetch: %w", err)
	}

	for i := range entries {
		entry := entries[i]
		if entry.SpeciesID != species {
			continue
		}

		if len(entry.Moveset) == 0 {
			return "", nil, fmt.Errorf("%w: %q", ErrNoRecommendedMoveset, species)
		}

		var charged []string
		if len(entry.Moveset) > 1 {
			charged = append(charged, entry.Moveset[1:]...)
		}

		return entry.Moveset[0], charged, nil
	}

	return "", nil, fmt.Errorf("%w: %q", ErrNoRecommendedMoveset, species)
}

// applyMovesetDefaults mutates the spec in place: if FastMove is
// empty it resolves the full recommended moveset from rankings and
// fills both slots. An empty ChargedMoves with a set FastMove is
// treated as a legitimate fast-only build (not every pokemon needs
// a charged move to simulate) and is left untouched — callers who
// want recommended charged moves too should leave FastMove empty as
// well. When the resolver fails the spec is left untouched and the
// error is returned so the caller can surface it.
//
// When disallowLegacy is true the resolved moveset is checked
// against the species' LegacyMoves list; if any of the pvpoke-
// recommended slots is legacy on this species the call fails with
// ErrLegacyConflict rather than silently filling in a move the
// caller explicitly rejected. The per-species lookup needs the
// gamemaster snapshot; pass nil when the caller is not enforcing
// the gate.
func applyMovesetDefaults(
	ctx context.Context, ranks *rankings.Manager,
	spec *Combatant, cpCap int, cup string,
	snapshot *pogopvp.Gamemaster, disallowLegacy bool,
) error {
	if spec.FastMove != "" {
		return nil
	}

	fast, charged, err := ResolveMoveset(ctx, ranks, spec.Species, cpCap, cup)
	if err != nil {
		return err
	}

	err = rejectResolvedLegacy(snapshot, spec.Species, fast, charged, disallowLegacy)
	if err != nil {
		return err
	}

	spec.FastMove = fast
	spec.ChargedMoves = charged

	return nil
}
