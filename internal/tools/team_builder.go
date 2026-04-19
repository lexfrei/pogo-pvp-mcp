package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

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
// Shields follows the same convention as [TeamAnalysisParams.Shields]:
// omit the field for the [1, 1] default, or supply both slots
// explicitly.
type TeamBuilderParams struct {
	Pool        []Combatant `json:"pool" jsonschema:"candidate combatants to draw the team from"`
	League      string      `json:"league" jsonschema:"little|great|ultra|master"`
	Cup         string      `json:"cup,omitempty" jsonschema:"cup id from pvpoke (e.g. spring, retro); empty = open-league all"`
	TopN        int         `json:"top_n,omitempty" jsonschema:"meta size for scoring (default 30)"`
	Shields     []int       `json:"shields,omitempty" jsonschema:"symmetric shield scenarios; omit for [1]; each 0..2"`
	MaxResults  int         `json:"max_results,omitempty" jsonschema:"how many top teams to return (default 5)"`
	Required    []string    `json:"required,omitempty" jsonschema:"species ids that must appear in the returned team"`
	Banned      []string    `json:"banned,omitempty" jsonschema:"species ids to exclude from the pool"`
	OptimizeFor string      `json:"optimize_for,omitempty" jsonschema:"overall|0s|1s|2s|all_pareto (default overall)"`
}

// TeamBuilderTeam is one candidate team plus its aggregated score.
// Members carries the resolved species+moveset triple (post-moveset
// defaulting) so the client sees exactly what was simulated, not
// just species ids. PoolIndices points back into TeamBuilderParams.Pool
// so callers can disambiguate duplicate species entries (same species
// id, different IV) — the species name in Members alone cannot
// identify which variant was chosen when a species appears more than
// once in the pool.
type TeamBuilderTeam struct {
	Members     []ResolvedCombatant `json:"members"`
	PoolIndices []int               `json:"pool_indices"`
	TeamScore   float64             `json:"team_score"`
	ParetoLabel string              `json:"pareto_label"`
}

// TeamBuilderResult is the JSON output for pvp_team_builder.
// SimulationFailures counts simulate calls that errored across the
// entire search; non-zero means some triples were scored with tie
// fallbacks and the ranking is less reliable.
type TeamBuilderResult struct {
	League             string            `json:"league"`
	Cup                string            `json:"cup"`
	CPCap              int               `json:"cp_cap"`
	PoolSize           int               `json:"pool_size"`
	Evaluated          int               `json:"evaluated_combinations"`
	SimulationFailures int               `json:"simulation_failures"`
	Teams              []TeamBuilderTeam `json:"teams"`
}

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

	inputs, err := tool.resolveTeamBuilderInputs(ctx, &params)
	if err != nil {
		return nil, TeamBuilderResult{}, err
	}

	result := evaluateTeams(ctx, inputs.pool, inputs.poolCombatants,
		inputs.metaCombatants, inputs.required, params.OptimizeFor)

	if ctx.Err() != nil {
		return nil, TeamBuilderResult{}, fmt.Errorf("team_builder cancelled: %w", ctx.Err())
	}

	sort.SliceStable(result.Teams, func(i, j int) bool {
		return result.Teams[i].TeamScore > result.Teams[j].TeamScore
	})

	result.Teams = result.Teams[:min(inputs.maxResults, len(result.Teams))]

	return nil, TeamBuilderResult{
		League:             inputs.league,
		Cup:                inputs.cup,
		CPCap:              inputs.cpCap,
		PoolSize:           len(inputs.pool),
		Evaluated:          result.Evaluated,
		SimulationFailures: result.Failures,
		Teams:              result.Teams,
	}, nil
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
	for i := range params.Pool {
		err := applyMovesetDefaults(ctx, tool.rankings, &params.Pool[i], cpCap, params.Cup)
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

		for jIdx := i + 1; jIdx < len(pool); jIdx++ {
			for kIdx := jIdx + 1; kIdx < len(pool); kIdx++ {
				speciesIDs := []string{pool[i].Species, pool[jIdx].Species, pool[kIdx].Species}
				if !containsAllSpecies(speciesIDs, required) {
					continue
				}

				score := scoreTripleFromMatrix(matrix, i, jIdx, kIdx, scenarioIdx)
				*evaluated++

				teams = append(teams, newTeam(pool, i, jIdx, kIdx, score, label))
			}
		}
	}

	return teams
}

// buildParetoFrontier returns up to four teams: one best-in-class per
// shield scenario plus a best-overall (averaged across all three
// scenarios). Duplicates across scenarios are schlopped — a single
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
		score := scoreTripleFromMatrix(matrix, i, jIdx, kIdx, scenarioIdx)

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
	overallScore := averageScenarioScore(matrix, i, jIdx, kIdx)

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

// newTeam assembles a TeamBuilderTeam from three pool indices.
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
// one triple — the overall-axis score in all_pareto mode.
func averageScenarioScore(
	matrix [][][scenarioCount]ratingMatrixEntry, iIdx, jIdx, kIdx int,
) float64 {
	var (
		total    float64
		counted  int
		anyScore bool
	)

	for scenarioIdx := range scenarioCount {
		score := scoreTripleFromMatrix(matrix, iIdx, jIdx, kIdx, scenarioIdx)
		if score == 0 {
			continue
		}

		total += score
		counted++
		anyScore = true
	}

	if !anyScore || counted == 0 {
		return 0
	}

	return total / float64(counted)
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
func precomputeRatingMatrix(
	ctx context.Context, poolCombatants, meta []pogopvp.Combatant,
) ratingMatrix {
	result := ratingMatrix{
		Entries: make([][][scenarioCount]ratingMatrixEntry, len(poolCombatants)),
	}

	for i := range poolCombatants {
		result.Entries[i] = make([][scenarioCount]ratingMatrixEntry, len(meta))

		if ctx.Err() != nil {
			return result
		}

		for oppIdx := range meta {
			for scenarioIdx := range scenarioCount {
				attacker := poolCombatants[i]
				defender := meta[oppIdx]
				attacker.Shields = scenarioIdx
				defender.Shields = scenarioIdx

				rating, err := ratingFor(&attacker, &defender)
				if err != nil {
					result.Failures++
					result.Entries[i][oppIdx][scenarioIdx] = ratingMatrixEntry{
						Rating: rating, OK: false,
					}

					continue
				}

				result.Entries[i][oppIdx][scenarioIdx] = ratingMatrixEntry{
					Rating: rating, OK: true,
				}
			}
		}
	}

	return result
}

// scoreTripleFromMatrix averages the ratings for three pool members
// against the full meta for one shield scenario, skipping failed
// entries in both numerator and denominator so failures do not
// distort the average toward ratingMidpoint.
func scoreTripleFromMatrix(
	matrix [][][scenarioCount]ratingMatrixEntry, iIdx, jIdx, kIdx, scenarioIdx int,
) float64 {
	if len(matrix) == 0 || len(matrix[iIdx]) == 0 {
		return 0
	}

	var (
		sum     float64
		counted int
	)

	for opp := range matrix[iIdx] {
		for _, member := range []int{iIdx, jIdx, kIdx} {
			entry := matrix[member][opp][scenarioIdx]
			if !entry.OK {
				continue
			}

			sum += float64(entry.Rating)
			counted++
		}
	}

	if counted == 0 {
		return 0
	}

	return sum / float64(counted)
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
