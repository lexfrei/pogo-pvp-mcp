package tools

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sort"
	"sync"
	"sync/atomic"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrPoolTooSmall is returned when the pool has fewer combatants than
// the team size ([TeamSize]).
var ErrPoolTooSmall = errors.New("pool too small")

// ErrPoolTooLarge is returned when the pool exceeds [MaxPoolSize] —
// the enumerator would otherwise run up to C(pool, 3) × meta
// simulations and can DOS the stdio server.
var ErrPoolTooLarge = errors.New("pool too large")

// ErrMaxResultsInvalid is returned when MaxResults is negative.
var ErrMaxResultsInvalid = errors.New("max_results must be non-negative")

// defaultMaxResults is the number of teams returned when the caller
// leaves MaxResults at zero.
const defaultMaxResults = 5

// MaxPoolSize caps the pool size for pvp_team_builder. The
// enumeration is O(pool^3 × meta) simulations; 50 keeps a worst-case
// top-30 request at ~600k sims — well under a second on the hot path
// with the precomputed ratings matrix.
const MaxPoolSize = 50

// TeamBuilderParams is the JSON input contract for pvp_team_builder.
// Shields is a list of symmetric shield scenarios (each entry 0..2;
// nil / empty → [1]). The first scenario seeds the pool/meta
// combatant shield count; the per-(pool, meta, scenario) matrix
// then scores each triple under every shield context regardless.
// OptimizeFor (overall|0s|1s|2s|all_pareto) selects which scenario
// axis drives the ranking; see TeamBuilderTeam.ParetoLabel.
//
// TargetLevel nuance:
//
//   - Omitted / zero — the cost helper computes the deepest 0.5-
//     grid level at which a 15/15/15 hundo of the same species
//     would still fit the league CP cap, and uses that as the
//     target. This is the "max powerup without busting cap" target
//     a competitive player would aim for.
//   - Positive — the exact 0.5-grid level is used verbatim as the
//     target (off-grid / out-of-range values rejected with
//     ErrInvalidTargetLevel).
//   - Members whose current level already reaches or exceeds the
//     resolved target get the already_at_or_above_target flag and
//     their powerup cost clamps to zero (no "negative cost" from
//     an inverted climb).
type TeamBuilderParams struct {
	Pool           []Combatant `json:"pool" jsonschema:"candidate combatants to draw the team from"`
	League         string      `json:"league" jsonschema:"little|great|ultra|master"`
	Cup            string      `json:"cup,omitempty" jsonschema:"cup id from pvpoke (e.g. spring, retro); empty = open-league all"`
	TopN           int         `json:"top_n,omitempty" jsonschema:"meta size for scoring (default 30)"`
	Shields        []int       `json:"shields,omitempty" jsonschema:"symmetric shield scenarios; omit for [1]; each 0..2"`
	MaxResults     int         `json:"max_results,omitempty" jsonschema:"how many top teams to return (default 5)"`
	Required       []string    `json:"required,omitempty" jsonschema:"species ids that must appear in the returned team"`
	Banned         []string    `json:"banned,omitempty" jsonschema:"species ids to exclude from the pool"`
	OptimizeFor    string      `json:"optimize_for,omitempty" jsonschema:"overall|0s|1s|2s|all_pareto (default overall)"`
	DisallowLegacy bool        `json:"disallow_legacy,omitempty" jsonschema:"reject pvpoke legacyMoves (permanently removed)"`
	DisallowElite  bool        `json:"disallow_elite,omitempty" jsonschema:"reject pvpoke eliteMoves (Elite TM / Community Day)"`
	TargetLevel    float64     `json:"target_level,omitempty" jsonschema:"cost target; 0 = deepest fit under cap; positive = 0.5-grid level"`
	AutoEvolve     bool        `json:"auto_evolve,omitempty" jsonschema:"walk each pool member to its terminal form under the cap"`
	Budget         *BudgetSpec `json:"budget,omitempty" jsonschema:"optional stardust budget; over-budget teams dropped"`
}

// BudgetSpec is the optional inventory cap applied to a team_builder
// enumeration. Today only StardustLimit is enforced — it compares
// the team's summed PowerupStardustCost + SecondMoveStardustCost
// against the limit and drops teams whose cost exceeds it.
//
// StardustTolerance is the fraction of over-budget still shown
// with a BudgetExceeded flag rather than dropped outright (default
// 0 = hard filter, no tolerance). Set e.g. 0.1 to keep teams up to
// 10% over budget in the result with BudgetExceeded=true; 0 or
// omitted drops them.
//
// R7.P3 added ETM enforcement: EliteChargedTM / EliteFastTM count
// the player's available Elite TMs; a team whose resolved moveset
// requires more ETMs than the budget allows is dropped (no
// tolerance — ETMs are whole-unit inventory). One ETM per elite
// move per team member: if a resolved ChargedMoves slot contains a
// pvpoke eliteMoves entry, that counts as one EliteChargedTM
// consumed; same for ElitEFastTM on a fast slot. Moves already
// accessible without an ETM (standard learnable or pvpoke
// legacyMoves — legacy is "permanently removed", the user either
// has one or can't get one, so an ETM doesn't help) are free.
// Counting is per team, so a resolved_moveset with AQUA_TAIL on
// Quagsire in all three Pareto teams counts as 1 ETM per team,
// not 3 across the run.
//
// XLCandy / RareCandyXL are parsed for contract stability but
// still NOT enforced: pool_breakdown.PowerupCandyCost is deferred
// to a future candy-cost branch (cross-source disagreement on
// per-half-level tiers), so XL candy totals can't be computed
// yet. Set them today to keep the input shape forward-compatible.
type BudgetSpec struct {
	StardustLimit     int     `json:"stardust,omitempty" jsonschema:"maximum total powerup + second-move stardust across the team"`
	StardustTolerance float64 `json:"stardust_tolerance,omitempty" jsonschema:"fraction over budget still kept with budget_exceeded flag"`
	EliteChargedTM    int     `json:"elite_charged_tm,omitempty" jsonschema:"Elite Charged TM inventory; teams needing more are dropped"`
	EliteFastTM       int     `json:"elite_fast_tm,omitempty" jsonschema:"Elite Fast TM inventory; teams needing more are dropped"`
	XLCandy           int     `json:"xl_candy,omitempty" jsonschema:"XL candy count available (not yet enforced — candy cost deferred)"`
	RareCandyXL       bool    `json:"rare_candy_xl,omitempty" jsonschema:"rare XL candy flag (not yet enforced)"`
}

// MemberCostBreakdown is the per-member cost estimate attached to
// every ResolvedCombatant in a team_builder response (Phase 3A).
// Stardust-only today — candy is still deferred to the separate
// candy-cost branch because public sources disagree on per-half-
// step values (see pvp_powerup_cost godoc). SecondMoveCandy IS
// emitted because pvpoke's buddy-distance derivation is
// unambiguous and already ships in pvp_second_move_cost.
//
// TargetLevel is the level the powerup climb targets. 0 + the
// AlreadyAtOrAbove flag means the member is already at or past
// the league's cap-fitting level; in that case the powerup cost
// fields clamp to zero so summing breakdowns across a team is
// straightforward for the client.
//
// Flags carries unstructured hints: "shadow_variant_missing" when
// Options.Shadow was set but pvpoke has no dedicated shadow row,
// "already_at_or_above_target" when the member needs no powerup.
type MemberCostBreakdown struct {
	TargetLevel              float64  `json:"target_level"`
	AlreadyAtOrAboveTarget   bool     `json:"already_at_or_above_target,omitempty"`
	PowerupStardustCost      int      `json:"powerup_stardust_cost"`
	PowerupStardustBaseline  int      `json:"powerup_stardust_baseline"`
	PowerupCrossesXLBoundary bool     `json:"powerup_crosses_xl_boundary,omitempty"`
	PowerupXLStepsIncluded   int      `json:"powerup_xl_steps_included,omitempty"`
	SecondMoveStardustCost   int      `json:"second_move_stardust_cost"`
	SecondMoveCandyCost      int      `json:"second_move_candy_cost"`
	SecondMoveCandyAvailable bool     `json:"second_move_candy_available"`
	SecondMoveStardustAvail  bool     `json:"second_move_stardust_available"`
	StardustMultiplier       float64  `json:"stardust_multiplier"`
	SecondMoveCostMultiplier float64  `json:"second_move_cost_multiplier"`
	Flags                    []string `json:"flags,omitempty"`
	// AutoEvolveAlternatives is populated only when the auto-evolve
	// pass hit branching at the FIRST hop with no prior linear
	// promotion (e.g. eevee → vaporeon / jolteon / flareon starting
	// from eevee itself). Each entry carries the child species id,
	// its predicted CP at the pool member's current level (evolution
	// preserves level in Pokémon GO), and whether that level-1
	// floor fits the league cap. R6.7 added Requirement per entry:
	// when the child species is in the curated evolution-item
	// table, the per-step item (if any) + candy cost + notes ship
	// alongside; otherwise Requirement is nil and the caller
	// should consult its own data source. Empty slice on non-
	// branching skips, on successful (full or partial) promotions,
	// AND on post-linear branching — the R7.P2 round-2 fix treats
	// branching AFTER an item-gated linear hop as a successful
	// partial promotion (see AutoEvolveRequirements below), so the
	// downstream branching alternatives are intentionally dropped
	// from the response in that case.
	AutoEvolveAlternatives []EvolveAlternative `json:"auto_evolve_alternatives,omitempty"`
	// AutoEvolveRequirements lists the evolution-item requirements
	// walkEvolutionChain accumulated on the linear path from the
	// original pool member to the post-evolve species (R7.P2). One
	// entry per item-gated step; silent gaps for steps whose
	// species are outside the curated table (bulbasaur → ivysaur,
	// etc.). Populated in three scenarios: (a) a successful full-
	// terminal promotion that touched ≥1 item-gated hop; (b) a
	// partial promotion that stopped at over-cap after ≥1 item-
	// gated hop; (c) a partial promotion that stopped at branching
	// after ≥1 item-gated hop (R7.P2 round-2 fix — the branching
	// alternatives are dropped from the response in this case;
	// AutoEvolveAlternatives stays empty). Empty when no evolution
	// happened, when the walk hit branching or over-cap at the
	// FIRST hop (base form untouched, skip flag in Flags + possibly
	// AutoEvolveAlternatives), or when the linear path had zero
	// item-gated steps.
	AutoEvolveRequirements []EvolutionItemRequirement `json:"auto_evolve_requirements,omitempty"`
}

// EvolveAlternative describes one branch the auto-evolve pass
// rejected because it could not pick unilaterally. When the branch
// is in the curated evolutionItemRequirements table (Bulbapedia-
// sourced subset of branching chains Niantic ships — gloom→
// vileplume/bellossom, slowpoke→slowking/slowbro, eevee split,
// etc.), Requirement carries the item (if any) and candy cost;
// otherwise Requirement is nil and callers should fall back to
// their own data source for per-step requirements.
type EvolveAlternative struct {
	To          string                    `json:"to"`
	PredictedCP int                       `json:"predicted_cp"`
	LeagueFit   bool                      `json:"league_fit"`
	Requirement *EvolutionItemRequirement `json:"requirement,omitempty"`
}

// EvolutionItemRequirement captures the item (if any) and candy
// cost a trainer must spend to perform one evolution step in
// Pokémon GO. Item is the canonical snake_case id; observed values
// in the table today: "sun_stone", "king_rock", "dragon_scale",
// "metal_coat", "up_grade", "sinnoh_stone", "mossy_lure",
// "glacial_lure", "magnetic_lure". Empty when no item is required
// (random-pick branches, stat-based splits like Tyrogue, buddy-km-
// gated eeveelutions). Candy is the per-step cost and is always
// positive in the current table. Notes is a free-form caveat for
// non-candy mechanics (buddy-km walk, time-of-day gate, lure-module
// requirement, mainline-vs-GO difference on Clamperl, etc.) and is
// empty when the entry has no further subtleties.
type EvolutionItemRequirement struct {
	Item  string `json:"item,omitempty"`
	Candy int    `json:"candy"`
	Notes string `json:"notes,omitempty"`
}

// ErrMemberInvalidForLeague is returned when a pool member's
// pre-simulation state already violates the league's CP cap (i.e.
// its level-1 CP at the given IVs exceeds the cap). The client is
// expected to fix the pool before retrying — the enumerator does
// not silently drop invalid entries because the "team of three"
// semantic breaks if some members are discarded.
var ErrMemberInvalidForLeague = errors.New("pool member is invalid for the league CP cap")

// ErrInvalidTargetLevel is returned when params.TargetLevel is set
// but does not land on the 0.5 grid within [1.0, 50.0]. The cost
// helpers would otherwise silently produce zero-cost breakdowns
// via the fall-through "pricing skipped" path; preferring a hard
// error matches pvp_powerup_cost's behaviour on the same check.
var ErrInvalidTargetLevel = errors.New("target_level must lie on the 0.5 grid within [1.0, 50.0]")

// TeamBuilderTeam is one candidate team plus its aggregated score.
// Members carries the resolved species+moveset triple (post-moveset
// defaulting) so the client sees exactly what was simulated, not
// just species ids. PoolIndices points back into TeamBuilderParams.Pool
// so callers can disambiguate duplicate species entries (same species
// id, different IV) — the species name in Members alone cannot
// identify which variant was chosen when a species appears more than
// once in the pool.
type TeamBuilderTeam struct {
	Members        []ResolvedCombatant   `json:"members"`
	CostBreakdowns []MemberCostBreakdown `json:"cost_breakdowns,omitempty"`
	PoolIndices    []int                 `json:"pool_indices"`
	TeamScore      float64               `json:"team_score"`
	ParetoLabel    string                `json:"pareto_label"`
	// AggregateCost is the sum of PowerupStardustCost +
	// SecondMoveStardustCost over every team member's
	// CostBreakdowns entry. Candy / XL-candy / ETM inventory is
	// NOT rolled in here — those are reported per-member in the
	// breakdown and summed by the caller if needed.
	AggregateCost  int  `json:"aggregate_stardust_cost"`
	BudgetExceeded bool `json:"budget_exceeded,omitempty"`
	BudgetExcess   int  `json:"budget_excess,omitempty"`
}

// TeamBuilderResult is the JSON output for pvp_team_builder.
// SimulationFailures counts individual Simulate calls that errored
// across the entire search across all three shield scenarios — one
// (pool, meta) pair can contribute up to 3 failures. Failed cells
// are excluded from both numerator and denominator when scoring a
// triple (no tie-midpoint fallback); a non-zero value just means the
// score sample for some triples was smaller than meta × scenarios
// would suggest.
//
// PoolMembers describes what the engine did with each input pool
// entry: kept as-is, promoted via auto_evolve, skipped branching,
// over-cap, or dropped by the banned filter. Helps callers debug
// "why isn't my Togetic in any returned team" without re-reading
// the flags scattered across per-member breakdowns.
type TeamBuilderResult struct {
	League             string             `json:"league"`
	Cup                string             `json:"cup"`
	CPCap              int                `json:"cp_cap"`
	PoolSize           int                `json:"pool_size"`
	Evaluated          int                `json:"evaluated_combinations"`
	SimulationFailures int                `json:"simulation_failures"`
	Teams              []TeamBuilderTeam  `json:"teams"`
	PoolMembers        []PoolMemberStatus `json:"pool_members,omitempty"`
}

// PoolMemberStatus is one row in TeamBuilderResult.PoolMembers: a
// per-pool-entry status report describing the engine's decision on
// that specific member. Keyed by Index (position in the caller's
// input Pool, preserved across auto_evolve / ban filtering).
type PoolMemberStatus struct {
	Index            int    `json:"index"`
	OriginalSpecies  string `json:"original_species"`
	ResolvedSpecies  string `json:"resolved_species"`
	AutoEvolveAction string `json:"auto_evolve_action"`
	Banned           bool   `json:"banned,omitempty"`
	InReturnedTeam   bool   `json:"in_returned_team"`
}

// AutoEvolve action constants used as PoolMemberStatus.AutoEvolve
// Action values. Hoisted so callers building their own tooling can
// switch on exact strings without guessing.
const (
	AutoEvolveActionKept             = "kept"
	AutoEvolveActionEvolved          = "evolved"
	AutoEvolveActionSkippedBranching = "skipped_branching"
	AutoEvolveActionSkippedOverCap   = "skipped_over_cap"
)

// TeamBuilderTool wraps the gamemaster and rankings managers.
type TeamBuilderTool struct {
	gm       *gamemaster.Manager
	rankings *rankings.Manager
}

// NewTeamBuilderTool constructs the tool bound to the given managers.
func NewTeamBuilderTool(gm *gamemaster.Manager, ranks *rankings.Manager) *TeamBuilderTool {
	return &TeamBuilderTool{gm: gm, rankings: ranks}
}

// teamBuilderDescription keeps the Tool struct literal within lll.
const teamBuilderDescription = "Enumerate 3-member teams from the candidate pool, score each against the top-N meta, " +
	"and return the highest-scoring teams. Honours required anchors and banned species."

// Tool returns the MCP tool registration.
func (tool *TeamBuilderTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_team_builder",
		Description: teamBuilderDescription,
	}
}

// Handler returns the MCP-typed handler.
func (tool *TeamBuilderTool) Handler() mcp.ToolHandlerFor[TeamBuilderParams, TeamBuilderResult] {
	return tool.handle
}

// teamBuilderInputs bundles the state the search helpers consume so
// handle stays under the funlen budget. required is keyed by species
// id so duplicate pool entries for the same species still satisfy an
// "at least one" anchor without forcing both into the team.
type teamBuilderInputs struct {
	pool           []Combatant
	poolCombatants []pogopvp.Combatant
	metaCombatants []pogopvp.Combatant
	required       map[string]struct{}
	cpCap          int
	league         string
	cup            string
	maxResults     int
}

// evaluationResult pairs the evaluated-combinations counter with the
// sorted candidate teams and the total number of failed simulate
// calls observed during the sweep.
type evaluationResult struct {
	Teams     []TeamBuilderTeam
	Evaluated int
	Failures  int
}

// handle orchestrates the team-builder search. It validates params,
// resolves pool / meta combatants, enumerates triples, and trims.
func (tool *TeamBuilderTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params TeamBuilderParams,
) (*mcp.CallToolResult, TeamBuilderResult, error) {
	err := ctx.Err()
	if err != nil {
		return nil, TeamBuilderResult{}, fmt.Errorf("team_builder cancelled: %w", err)
	}

	// Clone params.Pool up front so later preHandleValidation steps
	// (autoEvolvePool in particular) don't mutate caller memory. The
	// clone also gets originalIndex stamped on each entry so the
	// ban-filter + filterPool downstream copies still trace back to
	// the input position for pool-member status reporting (Finding
	// #6). Cheap: pool is bounded by MaxPoolSize=50.
	params.Pool = append([]Combatant(nil), params.Pool...)
	for i := range params.Pool {
		params.Pool[i].originalIndex = i
	}

	inputs, snapshot, err := tool.preHandleValidation(ctx, &params)
	if err != nil {
		return nil, TeamBuilderResult{}, err
	}

	result := evaluateTeams(ctx, inputs.pool, inputs.poolCombatants,
		inputs.metaCombatants, inputs.required, params.OptimizeFor)

	if ctx.Err() != nil {
		return nil, TeamBuilderResult{}, fmt.Errorf("team_builder cancelled: %w", ctx.Err())
	}

	poolMembers := tool.finaliseTeams(snapshot, &params, inputs, &result)

	return nil, TeamBuilderResult{
		League:             inputs.league,
		Cup:                inputs.cup,
		CPCap:              inputs.cpCap,
		PoolSize:           len(inputs.pool),
		Evaluated:          result.Evaluated,
		SimulationFailures: result.Failures,
		Teams:              result.Teams,
		PoolMembers:        poolMembers,
	}, nil
}

// finaliseTeams runs the post-enumeration pipeline: stable sort by
// score, apply the optional budget filter, trim to MaxResults,
// attach per-member cost breakdowns, capture PoolMemberStatus
// (using filtered-pool PoolIndices), and remap PoolIndices to
// original-pool coordinates for caller-visibility. Extracted from
// handle so the top-level stays under the funlen budget.
func (tool *TeamBuilderTool) finaliseTeams(
	snapshot *pogopvp.Gamemaster,
	params *TeamBuilderParams,
	inputs *teamBuilderInputs,
	result *evaluationResult,
) []PoolMemberStatus {
	_ = tool // receiver kept for symmetry with other helpers

	sort.SliceStable(result.Teams, func(i, j int) bool {
		return result.Teams[i].TeamScore > result.Teams[j].TeamScore
	})

	poolBreakdowns := computePoolBreakdowns(snapshot, inputs.pool, inputs.cpCap, params.TargetLevel)

	result.Teams = applyBudgetFilter(params, result.Teams, poolBreakdowns, snapshot)

	result.Teams = result.Teams[:min(inputs.maxResults, len(result.Teams))]

	attachCostBreakdownsFromPool(result.Teams, poolBreakdowns)

	// Pool-member status uses PoolIndices in filtered-pool coords
	// to compute in_returned_team; must run BEFORE the remap below.
	poolMembers := buildPoolMemberStatuses(params.Pool, params.Banned, inputs.pool, result.Teams)

	// Remap PoolIndices from filtered-pool to ORIGINAL-pool
	// coordinates so the caller-visible field shares one coordinate
	// system with PoolMembers.
	remapPoolIndicesToOriginal(result.Teams, inputs.pool)

	return poolMembers
}

// buildPoolMemberStatuses returns a per-pool-entry status snapshot
// that describes the engine's auto-evolve decision, any ban-filter
// exclusion, and whether the member appeared in any returned team.
// originalPool carries the caller's entries (post-auto-evolve
// mutation — autoEvolvedFrom still records the pre-evolve id).
// filteredPool is the ban-filtered slice PoolIndices points at;
// cross-referencing filteredPool[idx].originalIndex back into
// originalPool lets us set InReturnedTeam without guessing from
// species+IV matches.
func buildPoolMemberStatuses(
	originalPool []Combatant, banned []string,
	filteredPool []Combatant, teams []TeamBuilderTeam,
) []PoolMemberStatus {
	bannedSet := make(map[string]struct{}, len(banned))
	for _, id := range banned {
		bannedSet[id] = struct{}{}
	}

	inReturned := make(map[int]bool, len(filteredPool))

	for teamIdx := range teams {
		for _, poolIdx := range teams[teamIdx].PoolIndices {
			if poolIdx < 0 || poolIdx >= len(filteredPool) {
				continue
			}

			inReturned[filteredPool[poolIdx].originalIndex] = true
		}
	}

	out := make([]PoolMemberStatus, 0, len(originalPool))

	for i := range originalPool {
		spec := &originalPool[i]

		originalID := spec.Species
		if spec.autoEvolvedFrom != "" {
			originalID = spec.autoEvolvedFrom
		}

		// struct{}{} is the zero-value AND present-value of the set;
		// comma-ok is the only way to distinguish present from absent.
		_, bannedByOrig := bannedSet[originalID]
		_, bannedByResolved := bannedSet[spec.Species]

		out = append(out, PoolMemberStatus{
			Index:            i,
			OriginalSpecies:  originalID,
			ResolvedSpecies:  spec.Species,
			AutoEvolveAction: classifyAutoEvolveAction(spec),
			Banned:           bannedByOrig || bannedByResolved,
			InReturnedTeam:   inReturned[i],
		})
	}

	return out
}

// remapPoolIndicesToOriginal rewrites TeamBuilderTeam.PoolIndices
// from filtered-pool coordinates (what newTeam emits while the
// ranking matrix is laid out over the ban-filtered pool) to
// original-pool coordinates via filteredPool[idx].originalIndex.
// Mutates teams in place. Runs at the very end of handle so the
// caller-visible PoolIndices shares one coordinate system with
// PoolMembers; internal consumers (budget filter, cost attach)
// read the filtered-pool form earlier in the pipeline.
func remapPoolIndicesToOriginal(teams []TeamBuilderTeam, filteredPool []Combatant) {
	for teamIdx := range teams {
		indices := teams[teamIdx].PoolIndices

		for j, filteredIdx := range indices {
			if filteredIdx < 0 || filteredIdx >= len(filteredPool) {
				continue
			}

			indices[j] = filteredPool[filteredIdx].originalIndex
		}
	}
}

// classifyAutoEvolveAction maps the runtime-only autoEvolvedFrom +
// autoEvolveSkip state onto the exported AutoEvolveAction* constants
// used in PoolMemberStatus.
func classifyAutoEvolveAction(spec *Combatant) string {
	if spec.autoEvolvedFrom == "" {
		return AutoEvolveActionKept
	}

	if spec.autoEvolveSkip == "" {
		return AutoEvolveActionEvolved
	}

	// walkEvolutionChain is the only writer of autoEvolveSkip and
	// only ever emits skipReasonBranching or skipReasonOverCap
	// (the "" case is handled above as "evolved"). No default arm
	// needed — an unrecognised value is a programmer error, not a
	// state the tool contract exposes.
	if spec.autoEvolveSkip == skipReasonBranching {
		return AutoEvolveActionSkippedBranching
	}

	return AutoEvolveActionSkippedOverCap
}

// preHandleValidation bundles the cheap pre-simulation work that
// handle would otherwise inline: target_level grid / bounds check,
// pool resolution + moveset defaulting, snapshot acquisition, and
// the pool-fits-the-league check. Returning both the resolved
// inputs and the snapshot means the downstream cost-breakdown pass
// sees the same gamemaster pointer as everything else in the
// request, not a second .Current() read that could in principle
// observe a mid-refresh pointer.
func (tool *TeamBuilderTool) preHandleValidation(
	ctx context.Context, params *TeamBuilderParams,
) (*teamBuilderInputs, *pogopvp.Gamemaster, error) {
	err := validateTargetLevel(params.TargetLevel)
	if err != nil {
		return nil, nil, err
	}

	inputs, err := tool.resolveTeamBuilderInputs(ctx, params)
	if err != nil {
		return nil, nil, err
	}

	snapshot := tool.gm.Current()
	if snapshot == nil {
		return nil, nil, ErrGamemasterNotLoaded
	}

	err = validatePoolForLeague(inputs.pool, snapshot, inputs.cpCap)
	if err != nil {
		return nil, nil, err
	}

	return inputs, snapshot, nil
}

// resolveTeamBuilderInputs runs all validation, pool filtering, and
// combatant construction. Returned struct is consumed by handle.
func (tool *TeamBuilderTool) resolveTeamBuilderInputs(
	ctx context.Context, params *TeamBuilderParams,
) (*teamBuilderInputs, error) {
	err := validateTeamBuilderParams(params)
	if err != nil {
		return nil, err
	}

	snapshot := tool.gm.Current()
	if snapshot == nil {
		return nil, ErrGamemasterNotLoaded
	}

	cpCap, err := resolveCPCap(params.League, 0)
	if err != nil {
		return nil, err
	}

	err = rejectTeamRestricted(snapshot, params.Pool, params.DisallowLegacy, params.DisallowElite)
	if err != nil {
		return nil, err
	}

	if params.AutoEvolve {
		autoEvolvePool(snapshot, params.Pool, cpCap)
	}

	err = tool.defaultPoolMovesets(ctx, params, cpCap)
	if err != nil {
		return nil, err
	}

	return tool.buildTeamBuilderInputs(ctx, snapshot, params, cpCap)
}

// defaultPoolMovesets fills in the recommended moveset for every
// pool entry whose FastMove was omitted by the caller, so the rest
// of the pipeline operates on fully-specified combatants.
func (tool *TeamBuilderTool) defaultPoolMovesets(
	ctx context.Context, params *TeamBuilderParams, cpCap int,
) error {
	snapshot := tool.gm.Current()

	for i := range params.Pool {
		err := applyMovesetDefaults(ctx, tool.rankings, &params.Pool[i],
			cpCap, params.Cup, snapshot, params.DisallowLegacy, params.DisallowElite)
		if err != nil {
			return fmt.Errorf("pool[%d] moveset: %w", i, err)
		}
	}

	return nil
}

// buildTeamBuilderInputs runs the remaining resolution (pool filter,
// required species, meta slice) once the pool movesets are finalised.
func (tool *TeamBuilderTool) buildTeamBuilderInputs(
	ctx context.Context, snapshot *pogopvp.Gamemaster,
	params *TeamBuilderParams, cpCap int,
) (*teamBuilderInputs, error) {
	defaults := resolveTeamDefaults(params.Shields, params.TopN)

	pool, err := tool.preparePool(snapshot, params, defaults.Scenarios[0])
	if err != nil {
		return nil, err
	}

	required, err := resolveRequired(pool.Specs, params.Required)
	if err != nil {
		return nil, err
	}

	metaCombatants, err := tool.prepareMeta(ctx, snapshot, cpCap, params.Cup, defaults)
	if err != nil {
		return nil, err
	}

	maxResults := params.MaxResults
	if maxResults == 0 {
		maxResults = defaultMaxResults
	}

	return &teamBuilderInputs{
		pool:           pool.Specs,
		poolCombatants: pool.Combatants,
		metaCombatants: metaCombatants,
		required:       required,
		cpCap:          cpCap,
		league:         params.League,
		cup:            resolveCupLabel(params.Cup),
		maxResults:     maxResults,
	}, nil
}

// validateTeamBuilderParams runs the cheap pre-flight checks before any
// gamemaster or rankings lookups.
func validateTeamBuilderParams(params *TeamBuilderParams) error {
	if params.MaxResults < 0 {
		return fmt.Errorf("%w: %d", ErrMaxResultsInvalid, params.MaxResults)
	}

	if params.TopN < 0 {
		return fmt.Errorf("%w: %d must be non-negative", ErrInvalidTopN, params.TopN)
	}

	if len(params.Required) > TeamSize {
		return fmt.Errorf("%w: %d required species, only %d team slots",
			ErrTooManyRequired, len(params.Required), TeamSize)
	}

	return validateShields(params.Shields)
}

// ErrTooManyRequired is returned when Required has more entries than
// the team can fit — every triple fails the filter, which would
// otherwise produce an empty teams[] with no explanation.
var ErrTooManyRequired = errors.New("too many required species")

// preparedPool bundles the pool after filtering plus its matching
// engine-combatant slice so preparePool can return both without an
// unnamed multi-value signature.
type preparedPool struct {
	Specs      []Combatant
	Combatants []pogopvp.Combatant
}

// preparePool applies the banned filter and builds engine combatants
// for the surviving pool entries. Rejects inputs that are too small
// (below [TeamSize]) or too large (above [MaxPoolSize]) so the
// enumeration stays bounded.
func (tool *TeamBuilderTool) preparePool(
	snapshot *pogopvp.Gamemaster, params *TeamBuilderParams, shields int,
) (preparedPool, error) {
	if len(params.Pool) > MaxPoolSize {
		return preparedPool{}, fmt.Errorf("%w: have %d, max %d",
			ErrPoolTooLarge, len(params.Pool), MaxPoolSize)
	}

	specs := filterPool(params.Pool, params.Banned)
	if len(specs) < TeamSize {
		return preparedPool{}, fmt.Errorf("%w: have %d, need %d",
			ErrPoolTooSmall, len(specs), TeamSize)
	}

	combatants, err := buildTeamCombatants(snapshot, specs, shields)
	if err != nil {
		return preparedPool{}, err
	}

	return preparedPool{Specs: specs, Combatants: combatants}, nil
}

// prepareMeta fetches the ranking slice for the cap, trims to the
// configured top-N, and converts entries to engine combatants.
// Species missing from the current gamemaster (kept/rankings can
// diverge by up to a day during the refresh windows) are silently
// dropped from the returned combatants slice — the team_builder only
// consumes the combatants, never the entry metadata, so the skipped
// list is not surfaced separately here.
func (tool *TeamBuilderTool) prepareMeta(
	ctx context.Context, snapshot *pogopvp.Gamemaster,
	cpCap int, cup string, defaults teamAnalysisDefaults,
) ([]pogopvp.Combatant, error) {
	entries, err := tool.rankings.Get(ctx, cpCap, cup)
	if err != nil {
		return nil, fmt.Errorf("rankings fetch: %w", err)
	}

	metaEntries := entries[:min(defaults.TopN, len(entries))]

	combatants, _, _, err := buildMetaCombatants(snapshot, metaEntries, cpCap, defaults.Scenarios[0])

	return combatants, err
}

// filterPool drops any entries whose species appears in banned.
func filterPool(pool []Combatant, banned []string) []Combatant {
	if len(banned) == 0 {
		return pool
	}

	bannedSet := make(map[string]bool, len(banned))
	for _, id := range banned {
		bannedSet[id] = true
	}

	out := make([]Combatant, 0, len(pool))

	for i := range pool {
		if bannedSet[pool[i].Species] {
			continue
		}

		// When AutoEvolve promoted this pool entry the user's ban
		// may have been against the pre-evolution id; match both
		// the current Species and the original autoEvolvedFrom so
		// the ban honours the caller's intent without forcing them
		// to know the post-evolve species id in advance.
		if pool[i].autoEvolvedFrom != "" && bannedSet[pool[i].autoEvolvedFrom] {
			continue
		}

		out = append(out, pool[i])
	}

	return out
}

// ErrRequiredNotInPool is returned when a species listed in
// TeamBuilderParams.Required does not match any pool entry. Silent
// acceptance would run the search without the intended constraint and
// return teams that violate the caller's contract.
var ErrRequiredNotInPool = errors.New("required species missing from pool")

// resolveRequired validates that every required species id has at
// least one matching pool entry and returns the set of required
// species. The returned set drives triple filtering at the species
// level so a pool containing two copies of the same species still
// honours the "at least one of this species" semantic without forcing
// both copies into the team.
func resolveRequired(pool []Combatant, required []string) (map[string]struct{}, error) {
	out := make(map[string]struct{}, len(required))

	if len(required) == 0 {
		return out, nil
	}

	present := make(map[string]struct{}, len(pool))
	for i := range pool {
		present[pool[i].Species] = struct{}{}
	}

	for _, speciesID := range required {
		_, ok := present[speciesID]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrRequiredNotInPool, speciesID)
		}

		out[speciesID] = struct{}{}
	}

	return out, nil
}

// scenarioCount is the number of shield scenarios the per-scenario
// rating matrix covers: 0 shields vs 0, 1 vs 1, 2 vs 2. Used for
// the Pareto-frontier output in team_builder.
const scenarioCount = 3

// scenarioLabels maps scenario index to a stable human-readable
// label echoed back in TeamBuilderTeam.ParetoLabel.
//
//nolint:gochecknoglobals // fixed enumeration of shield scenarios, no reassignment
var scenarioLabels = [scenarioCount]string{
	"best 0-shield",
	"best 1-shield",
	"best 2-shield",
}

// evaluateTeams enumerates all 3-combinations of the pool that satisfy
// the required-species constraint and scores each triple. The score
// selection depends on params.OptimizeFor:
//   - "" or "overall" → average across the 1-shield scenario (default).
//   - "0s" / "1s" / "2s" → average across the named scenario only.
//   - "all_pareto" → for each of the three scenarios, pick the best
//     triple; dedup; add one "best overall" team too. Up to 4 teams.
func evaluateTeams(
	ctx context.Context,
	pool []Combatant,
	poolCombatants, meta []pogopvp.Combatant,
	required map[string]struct{},
	optimizeFor string,
) evaluationResult {
	matrix := precomputeRatingMatrix(ctx, poolCombatants, meta)

	out := evaluationResult{Failures: matrix.Failures}

	if optimizeFor == optimizeForAllPareto {
		out.Teams = buildParetoFrontier(ctx, pool, matrix.Entries, required, &out.Evaluated)

		return out
	}

	scenarioIdx, ok := selectScenario(optimizeFor)
	if !ok {
		// Unknown optimize_for falls back to overall (scenario 1).
		scenarioIdx = 1
	}

	label := "best overall"
	if optimizeFor == "0s" || optimizeFor == "1s" || optimizeFor == "2s" {
		label = scenarioLabels[scenarioIdx]
	}

	out.Teams = enumerateTeams(ctx, pool, matrix.Entries, required, scenarioIdx, label, &out.Evaluated)

	return out
}

// optimize_for recognised values. Anything else falls back to overall.
const (
	optimizeForOverall   = "overall"
	optimizeFor0s        = "0s"
	optimizeFor1s        = "1s"
	optimizeFor2s        = "2s"
	optimizeForAllPareto = "all_pareto"
)

// selectScenario maps optimize_for to a scenario index into the
// rating matrix. Returns false for all_pareto and unknown values; the
// caller decides the fallback.
func selectScenario(optimizeFor string) (int, bool) {
	switch optimizeFor {
	case optimizeFor0s:
		return 0, true
	case "", optimizeForOverall, optimizeFor1s:
		return 1, true
	case optimizeFor2s:
		return 2, true
	default:
		return 0, false
	}
}

// enumerateTeams walks every 3-combination of the pool satisfying
// `required`, scores each under the chosen scenario, and returns the
// resulting teams with the given label. Mutates *evaluated so the
// caller can tally how many triples were considered.
func enumerateTeams(
	ctx context.Context,
	pool []Combatant, matrix [][][scenarioCount]ratingMatrixEntry,
	required map[string]struct{},
	scenarioIdx int, label string, evaluated *int,
) []TeamBuilderTeam {
	var teams []TeamBuilderTeam

	for i := range pool {
		if ctx.Err() != nil {
			return teams
		}

		teams = enumerateFromAnchor(pool, matrix, required,
			scenarioIdx, label, i, evaluated, teams)
	}

	return teams
}

// enumerateFromAnchor extends the running `teams` slice with every
// triple that starts at pool[i] and satisfies the required-anchor
// set. Split out of [enumerateTeams] so the outer function keeps its
// ctx-cancellation check at the top while the inner loops stay under
// the gocognit budget.
func enumerateFromAnchor(
	pool []Combatant, matrix [][][scenarioCount]ratingMatrixEntry,
	required map[string]struct{},
	scenarioIdx int, label string,
	i int, evaluated *int, teams []TeamBuilderTeam,
) []TeamBuilderTeam {
	for jIdx := i + 1; jIdx < len(pool); jIdx++ {
		for kIdx := jIdx + 1; kIdx < len(pool); kIdx++ {
			speciesIDs := []string{pool[i].Species, pool[jIdx].Species, pool[kIdx].Species}
			if !containsAllSpecies(speciesIDs, required) {
				continue
			}

			score, ok := scoreTripleFromMatrix(matrix, i, jIdx, kIdx, scenarioIdx)
			*evaluated++

			if !ok {
				continue
			}

			teams = append(teams, newTeam(pool, i, jIdx, kIdx, score, label))
		}
	}

	return teams
}

// buildParetoFrontier returns up to four teams: one best-in-class per
// shield scenario plus a best-overall (averaged across all three
// scenarios). Duplicates across scenarios are dropped — a single
// team wins in e.g. both 0s and 1s and only appears once, labelled
// with the best scenario label it won.
func buildParetoFrontier(
	ctx context.Context,
	pool []Combatant, matrix [][][scenarioCount]ratingMatrixEntry,
	required map[string]struct{}, evaluated *int,
) []TeamBuilderTeam {
	bestByScenario := make([]TeamBuilderTeam, scenarioCount)
	foundByScenario := make([]bool, scenarioCount)

	var bestOverall TeamBuilderTeam

	foundOverall := false

	for i := range pool {
		if ctx.Err() != nil {
			break
		}

		for jIdx := i + 1; jIdx < len(pool); jIdx++ {
			for kIdx := jIdx + 1; kIdx < len(pool); kIdx++ {
				speciesIDs := []string{pool[i].Species, pool[jIdx].Species, pool[kIdx].Species}
				if !containsAllSpecies(speciesIDs, required) {
					continue
				}

				*evaluated++

				updateScenarioBests(pool, matrix, i, jIdx, kIdx,
					bestByScenario, foundByScenario)
				updateOverallBest(pool, matrix, i, jIdx, kIdx,
					&bestOverall, &foundOverall)
			}
		}
	}

	return assembleFrontier(bestByScenario, foundByScenario, bestOverall, foundOverall)
}

// updateScenarioBests checks the triple against the per-scenario
// champions and replaces them on improvement.
func updateScenarioBests(
	pool []Combatant, matrix [][][scenarioCount]ratingMatrixEntry,
	i, jIdx, kIdx int,
	bestByScenario []TeamBuilderTeam, foundByScenario []bool,
) {
	for scenarioIdx := range scenarioCount {
		score, ok := scoreTripleFromMatrix(matrix, i, jIdx, kIdx, scenarioIdx)
		if !ok {
			continue
		}

		if !foundByScenario[scenarioIdx] || score > bestByScenario[scenarioIdx].TeamScore {
			bestByScenario[scenarioIdx] = newTeam(pool, i, jIdx, kIdx,
				score, scenarioLabels[scenarioIdx])
			foundByScenario[scenarioIdx] = true
		}
	}
}

// updateOverallBest averages the triple's score across all three
// scenarios and replaces the running champion on improvement.
func updateOverallBest(
	pool []Combatant, matrix [][][scenarioCount]ratingMatrixEntry,
	i, jIdx, kIdx int,
	bestOverall *TeamBuilderTeam, foundOverall *bool,
) {
	overallScore, ok := averageScenarioScore(matrix, i, jIdx, kIdx)
	if !ok {
		return
	}

	if !*foundOverall || overallScore > bestOverall.TeamScore {
		*bestOverall = newTeam(pool, i, jIdx, kIdx, overallScore, "best overall")
		*foundOverall = true
	}
}

// assembleFrontier composes the final Pareto slice: overall first,
// then one scenario team per axis, de-duplicated by PoolIndices.
func assembleFrontier(
	bestByScenario []TeamBuilderTeam, foundByScenario []bool,
	bestOverall TeamBuilderTeam, foundOverall bool,
) []TeamBuilderTeam {
	var out []TeamBuilderTeam

	if foundOverall {
		out = append(out, bestOverall)
	}

	for scenarioIdx := range scenarioCount {
		if !foundByScenario[scenarioIdx] {
			continue
		}

		if duplicateInFrontier(out, bestByScenario[scenarioIdx].PoolIndices) {
			continue
		}

		out = append(out, bestByScenario[scenarioIdx])
	}

	return out
}

// duplicateInFrontier reports whether any existing team references
// the same pool triple. Order-insensitive since PoolIndices is
// always ascending (enumeration generates i<jIdx<kIdx).
func duplicateInFrontier(existing []TeamBuilderTeam, indices []int) bool {
	for i := range existing {
		if slices.Equal(existing[i].PoolIndices, indices) {
			return true
		}
	}

	return false
}

// newTeam assembles a TeamBuilderTeam from three filtered-pool
// indices. PoolIndices is initially populated in FILTERED-pool
// coordinates (what newTeam sees directly); downstream passes —
// budget filter + cost breakdown attach — rely on that coordinate
// system. handle() calls remapPoolIndicesToOriginal at the very
// end so the value that leaves the tool is in ORIGINAL-pool
// coordinates, aligned with TeamBuilderResult.PoolMembers.
func newTeam(
	pool []Combatant, i, jIdx, kIdx int, score float64, label string,
) TeamBuilderTeam {
	return TeamBuilderTeam{
		Members: []ResolvedCombatant{
			resolvedFromSpec(&pool[i]),
			resolvedFromSpec(&pool[jIdx]),
			resolvedFromSpec(&pool[kIdx]),
		},
		PoolIndices: []int{i, jIdx, kIdx},
		TeamScore:   score,
		ParetoLabel: label,
	}
}

// averageScenarioScore means-of-means across the three scenarios for
// one triple — the overall-axis score in all_pareto mode. Uses the
// ok signal from scoreTripleFromMatrix to distinguish a legitimate
// zero (full defender blowout on every matchup in the scenario)
// from "no valid sample" (every sim failed): only the former
// contributes to the mean. The second return value propagates the
// no-valid-sample signal to the caller so `updateOverallBest` does
// not promote a phantom 0-score team to "best overall" when every
// scenario came back as all-failures.
func averageScenarioScore(
	matrix [][][scenarioCount]ratingMatrixEntry, iIdx, jIdx, kIdx int,
) (float64, bool) {
	var (
		total   float64
		counted int
	)

	for scenarioIdx := range scenarioCount {
		score, ok := scoreTripleFromMatrix(matrix, iIdx, jIdx, kIdx, scenarioIdx)
		if !ok {
			continue
		}

		total += score
		counted++
	}

	if counted == 0 {
		return 0, false
	}

	return total / float64(counted), true
}

// ratingMatrixEntry pairs the rating with a flag that distinguishes a
// genuine tie/computed rating from a simulate failure. Failed entries
// are excluded from the score averages so a bad matchup does not
// pull the score toward the 500 midpoint.
type ratingMatrixEntry struct {
	Rating int
	OK     bool
}

// ratingMatrix is the result of a full (pool, meta) precompute: a
// per-scenario 3D cube of entries plus the total count of failed
// simulate calls observed across all three scenarios.
type ratingMatrix struct {
	Entries  [][][scenarioCount]ratingMatrixEntry
	Failures int
}

// precomputeRatingMatrix simulates every (pool_i, meta_j) pair across
// all three shield scenarios (0/0, 1/1, 2/2) exactly once, storing
// the per-scenario rating for reuse across every triple that uses
// pool_i. ctx cancellation short-circuits the computation.
//
// Rows are computed concurrently by a worker pool of size
// runtime.NumCPU(). Each pool-member row is fully independent (no
// shared state across i in the inner loops), so the only
// synchronisation needed is the semaphore channel + an atomic
// counter for cross-row Failures. Deterministic result: writes land
// at a row-specific slot, so the final matrix layout is identical
// to the sequential version regardless of completion order.
func precomputeRatingMatrix(
	ctx context.Context, poolCombatants, meta []pogopvp.Combatant,
) ratingMatrix {
	result := ratingMatrix{
		Entries: make([][][scenarioCount]ratingMatrixEntry, len(poolCombatants)),
	}

	if len(poolCombatants) == 0 {
		return result
	}

	workers := min(runtime.NumCPU(), len(poolCombatants))

	sem := make(chan struct{}, workers)

	var (
		wg          sync.WaitGroup
		failuresAll atomic.Int64
	)

	for i := range poolCombatants {
		if ctx.Err() != nil {
			break
		}

		result.Entries[i] = make([][scenarioCount]ratingMatrixEntry, len(meta))

		wg.Add(1)

		sem <- struct{}{} // acquire worker slot

		go func(rowIdx int) {
			defer wg.Done()
			defer func() { <-sem }()

			rowFails := fillRatingMatrixRow(ctx, poolCombatants[rowIdx], meta, result.Entries[rowIdx])
			failuresAll.Add(int64(rowFails))
		}(i)
	}

	wg.Wait()

	result.Failures = int(failuresAll.Load())

	return result
}

// fillRatingMatrixRow runs every (attacker, meta[oppIdx], scenario)
// simulation for one pool member and writes the results into row.
// Returns the number of simulate failures so the caller can
// aggregate them across rows atomically. Extracted from
// precomputeRatingMatrix so the parallel wrapper stays readable and
// the per-row logic is independently testable via the public handler
// path.
func fillRatingMatrixRow(
	ctx context.Context,
	attackerBase pogopvp.Combatant,
	meta []pogopvp.Combatant,
	row [][scenarioCount]ratingMatrixEntry,
) int {
	var fails int

	for oppIdx := range meta {
		if ctx.Err() != nil {
			return fails
		}

		for scenarioIdx := range scenarioCount {
			attacker := attackerBase
			defender := meta[oppIdx]
			attacker.Shields = scenarioIdx
			defender.Shields = scenarioIdx

			rating, err := ratingFor(&attacker, &defender)
			if err != nil {
				fails++
				row[oppIdx][scenarioIdx] = ratingMatrixEntry{Rating: rating, OK: false}

				continue
			}

			row[oppIdx][scenarioIdx] = ratingMatrixEntry{Rating: rating, OK: true}
		}
	}

	return fails
}

// scoreTripleFromMatrix averages the ratings for three pool members
// against the full meta for one shield scenario, skipping failed
// entries in both numerator and denominator so failures do not
// distort the average toward ratingMidpoint. The second return value
// is false when every (member, opponent) cell in the scenario was a
// simulate failure — callers must distinguish "legitimate score of
// 0" (full defender blowout) from "no valid sample" since both shape
// as numeric 0 and the callers' aggregation rules differ.
func scoreTripleFromMatrix(
	matrix [][][scenarioCount]ratingMatrixEntry, iIdx, jIdx, kIdx, scenarioIdx int,
) (float64, bool) {
	if len(matrix) == 0 || len(matrix[iIdx]) == 0 {
		return 0, false
	}

	var (
		sum     float64
		counted int
	)

	members := [3]int{iIdx, jIdx, kIdx}

	for opp := range matrix[iIdx] {
		for _, member := range members {
			entry := matrix[member][opp][scenarioIdx]
			if !entry.OK {
				continue
			}

			sum += float64(entry.Rating)
			counted++
		}
	}

	if counted == 0 {
		return 0, false
	}

	return sum / float64(counted), true
}

// containsAllSpecies reports whether every id in required is present
// in members. Empty required accepts any triple.
func containsAllSpecies(members []string, required map[string]struct{}) bool {
	for speciesID := range required {
		if !slices.Contains(members, speciesID) {
			return false
		}
	}

	return true
}
