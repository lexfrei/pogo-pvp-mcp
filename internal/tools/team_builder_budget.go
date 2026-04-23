package tools

import (
	pogopvp "github.com/lexfrei/pogo-pvp-engine"
)

// applyBudgetFilter is the post-enumeration pass that applies
// a caller-supplied BudgetSpec to the candidate teams. The flow:
//
//  1. For each team, aggregate PowerupStardustCost +
//     SecondMoveStardustCost across its three members using the
//     precomputed per-pool-member breakdowns. Store as
//     AggregateCost on the team.
//  2. If the team's resolved movesets require more Elite Charged
//     / Elite Fast TMs than BudgetSpec.EliteChargedTM /
//     EliteFastTM (R7.P3): drop. No tolerance — ETMs are whole-
//     unit inventory.
//  3. If AggregateCost > StardustLimit:
//     - within StardustLimit × (1 + Tolerance): keep, flag
//     BudgetExceeded=true + BudgetExcess=overBy.
//     - over the tolerance bound: drop entirely.
//
// Runs BEFORE the final trim so over-budget teams don't push the
// returned window into an empty slot. The poolBreakdowns slice is
// computed once by the caller (handle) and passed into both this
// filter and the downstream attach pass, so computeMemberCost
// runs exactly once per pool entry regardless of budget. The
// snapshot is needed for the ETM gate — per-move elite lookup is
// per-species (pogopvp.IsEliteMove).
func applyBudgetFilter(
	params *TeamBuilderParams, teams []TeamBuilderTeam,
	poolBreakdowns []MemberCostBreakdown,
	snapshot *pogopvp.Gamemaster,
) []TeamBuilderTeam {
	gates := resolveBudgetGates(params.Budget, snapshot)
	if !gates.stardust && !gates.etm {
		return teams
	}

	out := make([]TeamBuilderTeam, 0, len(teams))

	for i := range teams {
		kept, updated := evaluateTeamBudget(&teams[i], poolBreakdowns, snapshot, params.Budget, gates)
		if !kept {
			continue
		}

		out = append(out, updated)
	}

	return out
}

// budgetGates records which sub-filters of applyBudgetFilter are
// active for a given BudgetSpec. Both false = no Budget input →
// fast-exit path in applyBudgetFilter.
type budgetGates struct {
	stardust bool
	etm      bool
}

// resolveBudgetGates inspects BudgetSpec once and reports which
// gates need to fire per-team. Factored out so applyBudgetFilter
// stays under gocyclo.
func resolveBudgetGates(budget *BudgetSpec, snapshot *pogopvp.Gamemaster) budgetGates {
	if budget == nil {
		return budgetGates{}
	}

	return budgetGates{
		stardust: budget.StardustLimit > 0,
		etm:      snapshot != nil && (budget.EliteChargedTM > 0 || budget.EliteFastTM > 0),
	}
}

// evaluateTeamBudget is the per-team branch of applyBudgetFilter.
// Returns (keep, updatedTeam) so the outer loop stays slim.
func evaluateTeamBudget(
	team *TeamBuilderTeam, poolBreakdowns []MemberCostBreakdown,
	snapshot *pogopvp.Gamemaster, budget *BudgetSpec, gates budgetGates,
) (bool, TeamBuilderTeam) {
	out := *team
	out.AggregateCost = aggregateTeamStardustCost(poolBreakdowns, out.PoolIndices)

	if gates.etm && !teamFitsETMBudget(&out, snapshot, budget) {
		return false, out
	}

	if !gates.stardust {
		return true, out
	}

	return applyStardustGate(&out, budget)
}

// applyStardustGate applies the stardust limit + tolerance logic
// to one team. Returns (keep, updatedTeam). Factored out so
// applyBudgetFilter stays under funlen after the ETM gate landed.
func applyStardustGate(
	team *TeamBuilderTeam, budget *BudgetSpec,
) (bool, TeamBuilderTeam) {
	limit := budget.StardustLimit
	tolerance := budget.StardustTolerance

	if tolerance < 0 {
		tolerance = 0
	}

	hardCap := limit + int(float64(limit)*tolerance)

	out := *team

	if out.AggregateCost <= limit {
		return true, out
	}

	if out.AggregateCost > hardCap {
		return false, out
	}

	out.BudgetExceeded = true
	out.BudgetExcess = out.AggregateCost - limit

	return true, out
}

// teamFitsETMBudget returns true if the team's resolved movesets
// consume no more Elite Charged / Elite Fast TMs than BudgetSpec
// allows. Counts one ETM per elite move per member — an elite
// charged move in any resolved ChargedMoves slot costs 1 Elite
// Charged TM; an elite fast move in a FastMove slot costs 1
// Elite Fast TM. legacyMoves are NOT charged (they're
// permanently removed, ETM cannot teach them). Regular
// learnables cost nothing.
func teamFitsETMBudget(
	team *TeamBuilderTeam, snapshot *pogopvp.Gamemaster, budget *BudgetSpec,
) bool {
	chargedNeeded, fastNeeded := countTeamEliteMoves(team, snapshot)

	if budget.EliteChargedTM > 0 && chargedNeeded > budget.EliteChargedTM {
		return false
	}

	if budget.EliteFastTM > 0 && fastNeeded > budget.EliteFastTM {
		return false
	}

	return true
}

// countTeamEliteMoves tallies (chargedETMs, fastETMs) across a
// team's resolved movesets. Members whose species is missing
// from the snapshot contribute zero (cache-skew tolerance,
// matches the resolver convention).
//
//nolint:gocritic // unnamedResult: (charged, fast) documented in godoc
func countTeamEliteMoves(
	team *TeamBuilderTeam, snapshot *pogopvp.Gamemaster,
) (int, int) {
	var charged, fast int

	for i := range team.Members {
		member := &team.Members[i]

		lookupID := member.ResolvedSpeciesID
		if lookupID == "" {
			lookupID = member.Species
		}

		species, ok := snapshot.Pokemon[lookupID]
		if !ok {
			continue
		}

		if member.FastMove != "" && pogopvp.IsEliteMove(&species, member.FastMove) {
			fast++
		}

		for _, chargedID := range member.ChargedMoves {
			if pogopvp.IsEliteMove(&species, chargedID) {
				charged++
			}
		}
	}

	return charged, fast
}

// aggregateTeamStardustCost sums PowerupStardustCost +
// SecondMoveStardustCost across the three members identified by
// PoolIndices. Isolated so unit tests can exercise it without
// constructing a full team_builder workspace.
func aggregateTeamStardustCost(
	poolBreakdowns []MemberCostBreakdown, indices []int,
) int {
	var total int

	for _, idx := range indices {
		if idx < 0 || idx >= len(poolBreakdowns) {
			continue
		}

		total += poolBreakdowns[idx].PowerupStardustCost
		total += poolBreakdowns[idx].SecondMoveStardustCost
	}

	return total
}
