package tools

import (
	"fmt"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
)

// teamBuilderHundoIVs is the 15/15/15 spread used for LevelForCP
// when computing the default target level: the max level that a
// perfect-IV Pokémon fits under the CP cap. Callers whose members
// have sub-max IVs will therefore see a target_level that slightly
// underutilises their own IV spread — but using per-member IVs for
// the target introduces a subtle asymmetry (two identical-species
// members with different IVs get different target levels and
// therefore different powerup baselines). The hundo-IV choice
// means every member of the same species lands on the same target
// level, which matches how a human trainer plans ("I'll level this
// species to L48.5 in Great League") more than a per-IV tailoring
// would.
//
//nolint:gochecknoglobals // fixed domain constant
var teamBuilderHundoIVs = mustNewIV(pogopvp.MaxIV, pogopvp.MaxIV, pogopvp.MaxIV)

// mustNewIV is a test-only-allowed init-time helper. pogopvp.NewIV
// only returns an error when a component is out of [0, 15] range;
// hardcoded 15/15/15 is obviously valid. Wrap the error path in a
// panic so the package-level global above can be a `var` rather
// than a sync.Once lazy initialiser.
func mustNewIV(atk, def, sta int) pogopvp.IV {
	iv, err := pogopvp.NewIV(atk, def, sta)
	if err != nil {
		panic(fmt.Sprintf("mustNewIV(%d,%d,%d): %v", atk, def, sta, err))
	}

	return iv
}

// resolveTargetLevelForSpecies picks the level a team_builder
// powerup climb targets for a specific species. A caller-supplied
// explicit target wins outright. Otherwise the default is the
// highest 0.5-grid level at which a 15/15/15 spread fits under
// cpCap — i.e. the deepest climb a trainer would actually execute
// for this league. For master league (cap 10000, effectively
// uncapped) the default is MaxLevel (L50) since every IV reaches
// it.
func resolveTargetLevelForSpecies(cpCap int, base pogopvp.BaseStats, explicit float64) (float64, error) {
	if explicit > 0 {
		return explicit, nil
	}

	if cpCap >= masterLeagueCap {
		return pogopvp.MaxLevel, nil
	}

	spread, err := pogopvp.LevelForCP(base, teamBuilderHundoIVs, cpCap,
		pogopvp.FindSpreadOpts{XLAllowed: true})
	if err != nil {
		return 0, fmt.Errorf("target level for cap %d: %w", cpCap, err)
	}

	return spread.Level, nil
}

// validateMemberForLeague checks that a pool member's level-1 CP
// (the lowest CP it can be at the given IVs) still fits under the
// league cap. A species that produces CP > cap at level 1.0 cannot
// be in the team — clamping its target level downward wouldn't
// help. Surfaces ErrMemberInvalidForLeague with member index +
// species id so the client can fix the specific offending entry.
func validateMemberForLeague(
	idx int, spec *Combatant, species *pogopvp.Species, ivs pogopvp.IV, cpCap int,
) error {
	cpm, err := pogopvp.CPMAt(pogopvp.MinLevel)
	if err != nil {
		return fmt.Errorf("cpm at min level: %w", err)
	}

	baselineCP := pogopvp.ComputeCP(species.BaseStats, ivs, cpm)
	if baselineCP > cpCap {
		return fmt.Errorf("%w: team[%d] species=%q level-1 CP=%d exceeds cap=%d",
			ErrMemberInvalidForLeague, idx, spec.Species, baselineCP, cpCap)
	}

	return nil
}

// computeMemberCost builds the MemberCostBreakdown for one pool
// member against the per-species resolved target level.
//
// explicitTarget: 0 = use per-species default (deepest climb
// under cpCap with 15/15/15 IVs), non-zero = cap the climb at
// that exact level regardless of species.
//
// Stardust comes from powerup_cost (pre-XL + XL era, Options
// multipliers applied) and the second_move_cost integer arithmetic
// (thirdMoveCost + buddy distance derivation). Candy from
// second_move_cost only — powerup candy still deferred to the
// candy-audit branch.
//
// Over-target members clamp to zero cost with the
// already_at_or_above_target flag raised. computeMemberCost
// assumes a valid member — validatePoolForLeague must have run
// earlier.
func computeMemberCost(
	snapshot *pogopvp.Gamemaster, spec *Combatant, cpCap int, explicitTarget float64,
) MemberCostBreakdown {
	species, _, _, ok := resolveSpeciesLookup(snapshot, spec.Species, spec.Options)

	var targetLevel float64

	if ok {
		resolved, err := resolveTargetLevelForSpecies(cpCap, species.BaseStats, explicitTarget)
		if err == nil {
			targetLevel = resolved
		}
	}

	breakdown := MemberCostBreakdown{
		TargetLevel:        targetLevel,
		StardustMultiplier: powerupStardustMultiplierFor(spec.Options),
	}

	populatePowerupPortion(&breakdown, spec, targetLevel)

	populateSecondMovePortion(&breakdown, snapshot, spec)

	if spec.shadowVariantMissing {
		breakdown.Flags = append(breakdown.Flags, "shadow_variant_missing")
	}

	return breakdown
}

// populatePowerupPortion computes the stardust climb cost from the
// member's current level to the target level. If the member is
// already at or above the target, the powerup fields stay zero and
// the AlreadyAtOrAboveTarget flag is raised + surfaced in
// breakdown.Flags for discoverability.
func populatePowerupPortion(breakdown *MemberCostBreakdown, spec *Combatant, targetLevel float64) {
	if spec.Level >= targetLevel || targetLevel <= pogopvp.MinLevel {
		breakdown.AlreadyAtOrAboveTarget = true
		breakdown.Flags = append(breakdown.Flags, "already_at_or_above_target")

		return
	}

	fromIdx, toIdx, err := validatePowerupRange(spec.Level, targetLevel)
	if err != nil {
		// Off-grid current level; leave powerup fields at zero
		// rather than failing the whole cost computation. The
		// member-level enumeration proceeds; the flag on the
		// breakdown lets the client see that the climb was not
		// priced.
		breakdown.Flags = append(breakdown.Flags, fmt.Sprintf("powerup_pricing_skipped: %v", err))

		return
	}

	var baseline int

	for i := fromIdx; i < toIdx; i++ {
		baseline += powerupStardustTable[i]
	}

	scaled := scaleStardust(baseline, breakdown.StardustMultiplier)
	xlSteps := countXLSteps(fromIdx, toIdx)

	breakdown.PowerupStardustBaseline = baseline
	breakdown.PowerupStardustCost = scaled
	breakdown.PowerupCrossesXLBoundary = xlSteps > 0
	breakdown.PowerupXLStepsIncluded = xlSteps
}

// populateSecondMovePortion fills the second-move stardust +
// candy numbers. Lookup is the same shadow-aware resolve the
// pvp_second_move_cost tool uses, so Options.Shadow hits the
// shadow species' thirdMoveCost + buddy distance where pvpoke
// publishes them. Availability flags match the standalone tool's
// semantics (zero with availability=false means upstream data is
// missing).
func populateSecondMovePortion(
	breakdown *MemberCostBreakdown, snapshot *pogopvp.Gamemaster, spec *Combatant,
) {
	species, _, _, ok := resolveSpeciesLookup(snapshot, spec.Species, spec.Options)
	if !ok {
		return
	}

	multiplier := costMultiplierFor(spec.Options)
	breakdown.SecondMoveCostMultiplier = multiplier

	if species.ThirdMoveCost > 0 {
		breakdown.SecondMoveStardustCost = scaleCost(species.ThirdMoveCost, multiplier)
		breakdown.SecondMoveStardustAvail = true
	}

	candy, candyOK := candyCostFromBuddy(species.BuddyDistance)
	if candyOK {
		breakdown.SecondMoveCandyCost = scaleCost(candy, multiplier)
		breakdown.SecondMoveCandyAvailable = true
	}
}

// validatePoolForLeague walks the pool checking every member
// fits the league CP cap at level 1 and has well-formed IV /
// species. Runs before any simulation so a malformed pool fails
// fast with an actionable error rather than wasting cycles on a
// partial run.
func validatePoolForLeague(
	pool []Combatant, snapshot *pogopvp.Gamemaster, cpCap int,
) error {
	for i := range pool {
		spec := &pool[i]

		species, _, _, ok := resolveSpeciesLookup(snapshot, spec.Species, spec.Options)
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnknownSpecies, spec.Species)
		}

		ivs, err := pogopvp.NewIV(spec.IV[0], spec.IV[1], spec.IV[2])
		if err != nil {
			return fmt.Errorf("team[%d] invalid IV: %w", i, err)
		}

		err = validateMemberForLeague(i, spec, &species, ivs, cpCap)
		if err != nil {
			return err
		}
	}

	return nil
}

// computeTeamBreakdowns builds MemberCostBreakdown for each of the
// three team members, resolving a per-species target level when
// explicitTarget is zero. Returns a three-element slice aligned
// with team.Members.
func computeTeamBreakdowns(
	snapshot *pogopvp.Gamemaster, pool []Combatant,
	indices []int, cpCap int, explicitTarget float64,
) []MemberCostBreakdown {
	out := make([]MemberCostBreakdown, 0, len(indices))

	for _, idx := range indices {
		out = append(out, computeMemberCost(snapshot, &pool[idx], cpCap, explicitTarget))
	}

	return out
}

// attachCostBreakdowns walks every team in teams and populates its
// CostBreakdowns slice via computeTeamBreakdowns. Extracted from
// handle so the outer function stays under the funlen budget.
func attachCostBreakdowns(
	teams []TeamBuilderTeam, snapshot *pogopvp.Gamemaster,
	pool []Combatant, cpCap int, explicitTarget float64,
) {
	for i := range teams {
		teams[i].CostBreakdowns = computeTeamBreakdowns(
			snapshot, pool, teams[i].PoolIndices, cpCap, explicitTarget)
	}
}
