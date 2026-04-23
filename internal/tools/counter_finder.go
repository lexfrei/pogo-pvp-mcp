package tools

import (
	"context"
	"fmt"
	"sort"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CounterFinderParams is the JSON input contract for pvp_counter_finder.
// Target is required; FromPool is optional — an empty pool falls back
// to a top-N meta lookup via the rankings manager so the caller can
// ask "who in the current meta counters X" without a pokebox.
type CounterFinderParams struct {
	Target         Combatant   `json:"target" jsonschema:"the threat to find counters for"`
	FromPool       []Combatant `json:"from_pool,omitempty" jsonschema:"optional candidate pool; empty = scan top-N meta"`
	League         string      `json:"league" jsonschema:"little|great|ultra|master"`
	Cup            string      `json:"cup,omitempty" jsonschema:"cup id from pvpoke; empty = open-league all"`
	Shields        []int       `json:"shields,omitempty" jsonschema:"symmetric shield scenarios (omit for [1]); each 0..2"`
	TopN           int         `json:"top_n,omitempty" jsonschema:"how many counters to return (default 5)"`
	MetaTopN       int         `json:"meta_top_n,omitempty" jsonschema:"meta size when from_pool is empty (default 30)"`
	DisallowLegacy bool        `json:"disallow_legacy,omitempty" jsonschema:"reject legacyMoves in from_pool/meta; target passes as-is"`
	DisallowElite  bool        `json:"disallow_elite,omitempty" jsonschema:"reject eliteMoves in from_pool/meta; target passes as-is"`
}

// CounterScenarioResult is the per-scenario detail inside a
// CounterEntry: the integer battle rating plus post-simulate
// remaining HP for both sides. A scalar "remaining HP" is
// meaningless across shield scenarios, so we surface every
// scenario individually rather than mixing an averaged rating
// with a last-scenario HP pair.
type CounterScenarioResult struct {
	Shields            int `json:"shields"`
	Rating             int `json:"rating"`
	HPRemainingCounter int `json:"hp_remaining_counter"`
	HPRemainingTarget  int `json:"hp_remaining_target"`
}

// CounterEntry is one scored counter in the result. BattleRating
// is the mean across the scenario slice; ScenarioResults carries
// the per-scenario breakdown aligned with
// CounterFinderResult.Scenarios (same order, same length).
type CounterEntry struct {
	Counter         ResolvedCombatant       `json:"counter"`
	BattleRating    int                     `json:"battle_rating"`
	ScenarioResults []CounterScenarioResult `json:"scenario_results"`
}

// CounterFinderResult is the JSON output contract.
type CounterFinderResult struct {
	Target             ResolvedCombatant `json:"target"`
	League             string            `json:"league"`
	Cup                string            `json:"cup"`
	Scenarios          []int             `json:"scenarios"`
	Counters           []CounterEntry    `json:"counters"`
	SimulationFailures int               `json:"simulation_failures"`
}

// CounterFinderTool wraps the gamemaster + rankings managers.
type CounterFinderTool struct {
	gm       *gamemaster.Manager
	rankings *rankings.Manager
}

// NewCounterFinderTool constructs the tool bound to the managers.
func NewCounterFinderTool(gm *gamemaster.Manager, ranks *rankings.Manager) *CounterFinderTool {
	return &CounterFinderTool{gm: gm, rankings: ranks}
}

const counterFinderToolDescription = "Find the top-N counters to a given threat — either from a user-supplied " +
	"pool of combatants, or from the top-N pvpoke meta. Ratings are averaged across the requested shield scenarios."

// Tool returns the MCP tool registration.
func (tool *CounterFinderTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_counter_finder",
		Description: counterFinderToolDescription,
	}
}

// Handler returns the MCP-typed handler function.
func (tool *CounterFinderTool) Handler() mcp.ToolHandlerFor[CounterFinderParams, CounterFinderResult] {
	return tool.handle
}

// validateCounterFinderParams runs the cheap pre-flight checks
// (cancel, TopN/MetaTopN ranges, FromPool size under MaxPoolSize,
// shields shape) before any gamemaster or rankings lookup. Keeps
// the main handler body within funlen.
func validateCounterFinderParams(ctx context.Context, params *CounterFinderParams) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("counter_finder cancelled: %w", err)
	}

	if params.TopN < 0 {
		return fmt.Errorf("%w: top_n %d must be non-negative",
			ErrInvalidTopN, params.TopN)
	}

	if params.MetaTopN < 0 {
		return fmt.Errorf("%w: meta_top_n %d must be non-negative",
			ErrInvalidTopN, params.MetaTopN)
	}

	if len(params.FromPool) > MaxPoolSize {
		return fmt.Errorf("%w: from_pool size %d exceeds MaxPoolSize %d",
			ErrPoolTooLarge, len(params.FromPool), MaxPoolSize)
	}

	return validateShields(params.Shields)
}

// defaultCounterFinderTopN is the counter count returned when the
// caller leaves TopN at zero.
const defaultCounterFinderTopN = 5

// defaultCounterFinderMetaTopN is the meta-window size used when
// FromPool is empty.
const defaultCounterFinderMetaTopN = 30

// counterFinderWorkspace bundles the resolved state the simulation
// phase consumes. Splits handle into prepare + simulate so the
// funlen budget stays under control.
type counterFinderWorkspace struct {
	target     pogopvp.Combatant
	targetSpec Combatant
	candidates []pogopvp.Combatant
	specs      []Combatant
	scenarios  []int
}

// handle orchestrates counter discovery: resolve target + candidate
// pool, simulate each candidate vs target across scenarios, sort
// descending by rating, trim to TopN. Ctx cancellation is polled at
// the candidate loop boundary.
func (tool *CounterFinderTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params CounterFinderParams,
) (*mcp.CallToolResult, CounterFinderResult, error) {
	err := validateCounterFinderParams(ctx, &params)
	if err != nil {
		return nil, CounterFinderResult{}, err
	}

	workspace, err := tool.prepareCounterFinder(ctx, &params)
	if err != nil {
		return nil, CounterFinderResult{}, err
	}

	counters, failures := tool.scoreCandidates(ctx, workspace)

	sort.SliceStable(counters, func(i, j int) bool {
		return counters[i].BattleRating > counters[j].BattleRating
	})

	topN := params.TopN
	if topN == 0 {
		topN = defaultCounterFinderTopN
	}

	counters = counters[:min(topN, len(counters))]

	return nil, CounterFinderResult{
		Target:             resolvedFromSpec(&workspace.targetSpec),
		League:             params.League,
		Cup:                resolveCupLabel(params.Cup),
		Scenarios:          workspace.scenarios,
		Counters:           counters,
		SimulationFailures: failures,
	}, nil
}

// prepareCounterFinder resolves gamemaster snapshot, CP cap, target
// moveset (auto-filled when empty), scenarios, and the candidate
// pool. When FromPool is empty it pulls the top-N meta from the
// rankings manager and builds combatants at the pvpoke-ranking
// level cap, matching team_analysis convention.
func (tool *CounterFinderTool) prepareCounterFinder(
	ctx context.Context, params *CounterFinderParams,
) (*counterFinderWorkspace, error) {
	snapshot := tool.gm.Current()
	if snapshot == nil {
		return nil, ErrGamemasterNotLoaded
	}

	cpCap, err := resolveCPCap(params.League, 0)
	if err != nil {
		return nil, err
	}

	scenarios := resolveTeamDefaults(params.Shields, 0).Scenarios

	// Target describes the ENEMY, not the caller's own Pokémon.
	// Applying disallow_legacy / disallow_elite to the target would
	// reject real ladder builds (e.g. Serperior with FRENZY_PLANT,
	// Lapras with its elite chargeds) that the opponent actively
	// uses, producing a weakened-enemy counter list — the opposite
	// of what the caller wants. Flags apply only to the from_pool /
	// meta-fallback candidates downstream (the Pokémon the caller
	// will field in response). Target moveset is auto-filled from
	// pvpoke's recommendation as-is, no category filter — the
	// hardcoded (false, false) below is deliberate: passing the
	// caller's flags here would re-introduce r7 finding #13.
	const targetDisallowLegacy, targetDisallowElite = false, false

	err = applyMovesetDefaults(ctx, tool.rankings, &params.Target, cpCap, params.Cup,
		snapshot, targetDisallowLegacy, targetDisallowElite)
	if err != nil {
		return nil, fmt.Errorf("target moveset: %w", err)
	}

	target, err := buildEngineCombatant(snapshot, &params.Target, scenarios[0])
	if err != nil {
		return nil, fmt.Errorf("target combatant: %w", err)
	}

	candidates, specs, err := tool.resolveCandidates(ctx, snapshot, params, cpCap, scenarios[0])
	if err != nil {
		return nil, err
	}

	return &counterFinderWorkspace{
		target:     target,
		targetSpec: params.Target,
		candidates: candidates,
		specs:      specs,
		scenarios:  scenarios,
	}, nil
}

// resolveCandidates produces the (combatants, specs, err) triple the
// simulate loop consumes. FromPool wins when non-empty; otherwise
// the rankings manager supplies the top-N meta (default 30).
//
//nolint:gocritic // unnamedResult: triple documented on the doc line
func (tool *CounterFinderTool) resolveCandidates(
	ctx context.Context, snapshot *pogopvp.Gamemaster,
	params *CounterFinderParams, cpCap, shields int,
) ([]pogopvp.Combatant, []Combatant, error) {
	if len(params.FromPool) > 0 {
		err := rejectTeamRestricted(snapshot, params.FromPool, params.DisallowLegacy, params.DisallowElite)
		if err != nil {
			return nil, nil, err
		}

		for i := range params.FromPool {
			err = applyMovesetDefaults(ctx, tool.rankings, &params.FromPool[i], cpCap, params.Cup,
				snapshot, params.DisallowLegacy, params.DisallowElite)
			if err != nil {
				return nil, nil, fmt.Errorf("from_pool[%d]: %w", i, err)
			}
		}

		combatants, err := buildTeamCombatants(snapshot, params.FromPool, shields)
		if err != nil {
			return nil, nil, err
		}

		return combatants, params.FromPool, nil
	}

	return tool.resolveMetaCandidates(ctx, snapshot, params, cpCap, shields)
}

// resolveMetaCandidates pulls the top-N meta from rankings and
// converts entries into engine combatants + echo specs. Separate
// from resolveCandidates so the FromPool and meta branches stay
// visually distinct.
//
//nolint:gocritic // unnamedResult: (combatants, specs, err) documented above
func (tool *CounterFinderTool) resolveMetaCandidates(
	ctx context.Context, snapshot *pogopvp.Gamemaster,
	params *CounterFinderParams, cpCap, shields int,
) ([]pogopvp.Combatant, []Combatant, error) {
	metaTopN := params.MetaTopN
	if metaTopN == 0 {
		metaTopN = defaultCounterFinderMetaTopN
	}

	entries, err := tool.rankings.Get(ctx, cpCap, params.Cup)
	if err != nil {
		return nil, nil, fmt.Errorf("rankings fetch: %w", err)
	}

	metaEntries := entries[:min(metaTopN, len(entries))]

	if params.DisallowLegacy {
		metaEntries = filterRestrictedMetaEntries(snapshot, metaEntries, legacyCategory)
	}

	if params.DisallowElite {
		metaEntries = filterRestrictedMetaEntries(snapshot, metaEntries, eliteCategory)
	}

	combatants, kept, _, err := buildMetaCombatants(snapshot, metaEntries, cpCap, shields)
	if err != nil {
		return nil, nil, err
	}

	return combatants, specsFromMetaEntries(kept), nil
}

// specsFromMetaEntries projects pvpoke ranking entries into the
// tool's Combatant shape. Used by resolveMetaCandidates to echo
// the auto-resolved species + moveset back in the counter-finder
// response. Factored out to keep resolveMetaCandidates under funlen.
func specsFromMetaEntries(entries []rankings.RankingEntry) []Combatant {
	out := make([]Combatant, 0, len(entries))

	for i := range entries {
		entry := entries[i]

		var fast string
		if len(entry.Moveset) > 0 {
			fast = entry.Moveset[0]
		}

		spec := Combatant{
			Species:  entry.SpeciesID,
			FastMove: fast,
		}

		if len(entry.Moveset) > 1 {
			spec.ChargedMoves = append(spec.ChargedMoves, entry.Moveset[1:]...)
		}

		out = append(out, spec)
	}

	return out
}

// filterRestrictedMetaEntries drops ranking entries whose pvpoke
// recommended moveset contains a move in the given restricted
// category (legacy or elite) for the species. Used by the meta-
// fallback branch of pvp_counter_finder to honour DisallowLegacy /
// DisallowElite the same way the explicit-pool branch does via
// rejectTeamRestricted. Entries whose species is missing from the
// snapshot are kept — the downstream buildMetaCombatants will skip
// them with ErrUnknownSpecies in line with the existing tolerance
// for gamemaster / rankings cache skew.
func filterRestrictedMetaEntries(
	snapshot *pogopvp.Gamemaster, entries []rankings.RankingEntry, cat restrictedCategory,
) []rankings.RankingEntry {
	out := make([]rankings.RankingEntry, 0, len(entries))

	for i := range entries {
		species, ok := snapshot.Pokemon[entries[i].SpeciesID]
		if ok && movesetInRestrictedCategory(&species, entries[i].Moveset, cat) {
			continue
		}

		out = append(out, entries[i])
	}

	return out
}

// scoreCandidates runs the core simulation sweep: for every
// candidate pair against the shared target, compute the averaged
// rating + per-scenario breakdown, collect. Returns (entries,
// failures). ctx.Err() is checked at the candidate boundary so
// cancellation promptly releases.
//
//nolint:gocritic // unnamedResult: (entries, failures) documented above
func (tool *CounterFinderTool) scoreCandidates(
	ctx context.Context, workspace *counterFinderWorkspace,
) ([]CounterEntry, int) {
	out := make([]CounterEntry, 0, len(workspace.candidates))

	var failures int

	for i := range workspace.candidates {
		if ctx.Err() != nil {
			return out, failures
		}

		entry, ok := scoreCounter(&workspace.candidates[i], &workspace.target,
			&workspace.specs[i], workspace.scenarios)
		if !ok {
			failures++

			continue
		}

		out = append(out, entry)
	}

	return out, failures
}

// scoreCounter simulates one candidate × target across each shield
// scenario via a single pogopvp.Simulate call per scenario (no
// double-dispatch through ratingFor). Returns ok=false when every
// scenario failed to simulate; otherwise BattleRating is the mean
// rating over the scenarios that did simulate, and ScenarioResults
// is aligned 1:1 with the scenarios slice for the scenarios that
// succeeded.
func scoreCounter(
	candidate, target *pogopvp.Combatant,
	spec *Combatant, scenarios []int,
) (CounterEntry, bool) {
	results := make([]CounterScenarioResult, 0, len(scenarios))

	var (
		sum     int
		counted int
	)

	for _, shields := range scenarios {
		att := *candidate
		def := *target
		att.Shields = shields
		def.Shields = shields

		result, err := pogopvp.Simulate(&att, &def, pogopvp.BattleOptions{})
		if err != nil {
			continue
		}

		rating := ratingFromResult(&att, &def, &result)

		results = append(results, CounterScenarioResult{
			Shields:            shields,
			Rating:             rating,
			HPRemainingCounter: result.HPRemaining[0],
			HPRemainingTarget:  result.HPRemaining[1],
		})
		sum += rating
		counted++
	}

	if counted == 0 {
		return CounterEntry{}, false
	}

	return CounterEntry{
		Counter:         resolvedFromSpec(spec),
		BattleRating:    sum / counted,
		ScenarioResults: results,
	}, true
}
