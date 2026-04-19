package tools

// applyBudgetFilter is the post-enumeration pass that applies
// a caller-supplied BudgetSpec to the candidate teams. The flow:
//
//  1. For each team, aggregate PowerupStardustCost +
//     SecondMoveStardustCost across its three members using the
//     precomputed per-pool-member breakdowns. Store as
//     AggregateCost on the team.
//  2. If AggregateCost > StardustLimit:
//     - within StardustLimit × (1 + Tolerance): keep, flag
//     BudgetExceeded=true + BudgetExcess=overBy.
//     - over the tolerance bound: drop entirely.
//
// Runs BEFORE the final trim so over-budget teams don't push the
// returned window into an empty slot. The poolBreakdowns slice is
// computed once by the caller (handle) and passed into both this
// filter and the downstream attach pass, so computeMemberCost
// runs exactly once per pool entry regardless of budget.
func applyBudgetFilter(
	params *TeamBuilderParams, teams []TeamBuilderTeam,
	poolBreakdowns []MemberCostBreakdown,
) []TeamBuilderTeam {
	if params.Budget == nil || params.Budget.StardustLimit <= 0 {
		return teams
	}

	limit := params.Budget.StardustLimit
	tolerance := params.Budget.StardustTolerance

	if tolerance < 0 {
		tolerance = 0
	}

	hardCap := limit + int(float64(limit)*tolerance)

	out := make([]TeamBuilderTeam, 0, len(teams))

	for i := range teams {
		team := teams[i]
		team.AggregateCost = aggregateTeamStardustCost(poolBreakdowns, team.PoolIndices)

		if team.AggregateCost <= limit {
			out = append(out, team)

			continue
		}

		if team.AggregateCost > hardCap {
			continue
		}

		team.BudgetExceeded = true
		team.BudgetExcess = team.AggregateCost - limit
		out = append(out, team)
	}

	return out
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
