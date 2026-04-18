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

// ErrMaxResultsInvalid is returned when MaxResults is negative.
var ErrMaxResultsInvalid = errors.New("max_results must be non-negative")

// defaultMaxResults is the number of teams returned when the caller
// leaves MaxResults at zero.
const defaultMaxResults = 5

// TeamBuilderParams is the JSON input contract for pvp_team_builder.
// Shields follows the same convention as [TeamAnalysisParams.Shields]:
// omit the field for the [1, 1] default, or supply both slots
// explicitly.
type TeamBuilderParams struct {
	Pool       []Combatant `json:"pool" jsonschema:"candidate combatants to draw the team from"`
	League     string      `json:"league" jsonschema:"great|ultra|master"`
	TopN       int         `json:"top_n,omitempty" jsonschema:"meta size for scoring (default 30)"`
	Shields    []int       `json:"shields,omitempty" jsonschema:"[team, meta] shield counts; omit for [1, 1]; each 0..2"`
	MaxResults int         `json:"max_results,omitempty" jsonschema:"how many top teams to return (default 5)"`
	Required   []string    `json:"required,omitempty" jsonschema:"species ids that must appear in the returned team"`
	Banned     []string    `json:"banned,omitempty" jsonschema:"species ids to exclude from the pool"`
}

// TeamBuilderTeam is one candidate team plus its aggregated score.
type TeamBuilderTeam struct {
	Members   []string `json:"members"`
	TeamScore float64  `json:"team_score"`
	Reason    string   `json:"reason"`
}

// TeamBuilderResult is the JSON output for pvp_team_builder.
type TeamBuilderResult struct {
	League    string            `json:"league"`
	CPCap     int               `json:"cp_cap"`
	PoolSize  int               `json:"pool_size"`
	Evaluated int               `json:"evaluated_combinations"`
	Teams     []TeamBuilderTeam `json:"teams"`
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
	maxResults     int
}

// evaluationResult pairs the evaluated-combinations counter with the
// sorted candidate teams.
type evaluationResult struct {
	Teams     []TeamBuilderTeam
	Evaluated int
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

	result := evaluateTeams(inputs.pool, inputs.poolCombatants, inputs.metaCombatants, inputs.required)

	sort.SliceStable(result.Teams, func(i, j int) bool {
		return result.Teams[i].TeamScore > result.Teams[j].TeamScore
	})

	result.Teams = result.Teams[:min(inputs.maxResults, len(result.Teams))]

	return nil, TeamBuilderResult{
		League:    inputs.league,
		CPCap:     inputs.cpCap,
		PoolSize:  len(inputs.pool),
		Evaluated: result.Evaluated,
		Teams:     result.Teams,
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

	defaults := resolveTeamDefaults(params.Shields, params.TopN)

	pool, err := tool.preparePool(snapshot, params, defaults.TeamShields)
	if err != nil {
		return nil, err
	}

	required, err := resolveRequired(pool.Specs, params.Required)
	if err != nil {
		return nil, err
	}

	metaCombatants, err := tool.prepareMeta(ctx, snapshot, cpCap, defaults)
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

	return validateShields(params.Shields)
}

// preparedPool bundles the pool after filtering plus its matching
// engine-combatant slice so preparePool can return both without an
// unnamed multi-value signature.
type preparedPool struct {
	Specs      []Combatant
	Combatants []pogopvp.Combatant
}

// preparePool applies the banned filter and builds engine combatants
// for the surviving pool entries.
func (tool *TeamBuilderTool) preparePool(
	snapshot *pogopvp.Gamemaster, params *TeamBuilderParams, shields int,
) (preparedPool, error) {
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
func (tool *TeamBuilderTool) prepareMeta(
	ctx context.Context, snapshot *pogopvp.Gamemaster,
	cpCap int, defaults teamAnalysisDefaults,
) ([]pogopvp.Combatant, error) {
	entries, err := tool.rankings.Get(ctx, cpCap)
	if err != nil {
		return nil, fmt.Errorf("rankings fetch: %w", err)
	}

	metaEntries := entries[:min(defaults.TopN, len(entries))]

	return buildMetaCombatants(snapshot, metaEntries, cpCap, defaults.MetaShields)
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

// evaluateTeams enumerates all 3-combinations of the pool that satisfy
// the required-species constraint and returns them annotated with
// team_score. Scoring uses the shared ratingFor helper.
func evaluateTeams(
	pool []Combatant,
	poolCombatants, meta []pogopvp.Combatant,
	required map[string]struct{},
) evaluationResult {
	var out evaluationResult

	for i := range pool {
		for jIdx := i + 1; jIdx < len(pool); jIdx++ {
			for kIdx := jIdx + 1; kIdx < len(pool); kIdx++ {
				members := []string{pool[i].Species, pool[jIdx].Species, pool[kIdx].Species}
				if !containsAllSpecies(members, required) {
					continue
				}

				teamCombatants := []pogopvp.Combatant{
					poolCombatants[i], poolCombatants[jIdx], poolCombatants[kIdx],
				}
				score := scoreTeam(teamCombatants, meta)
				out.Evaluated++

				out.Teams = append(out.Teams, TeamBuilderTeam{
					Members:   members,
					TeamScore: score,
					Reason:    "highest average battle rating across the sampled meta",
				})
			}
		}
	}

	return out
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

// scoreTeam returns the average battle rating across the cartesian
// product of team × meta.
func scoreTeam(team, meta []pogopvp.Combatant) float64 {
	if len(team) == 0 || len(meta) == 0 {
		return 0
	}

	var sum float64

	for memberIdx := range team {
		for oppIdx := range meta {
			sum += float64(ratingFor(&team[memberIdx], &meta[oppIdx]))
		}
	}

	return sum / float64(len(team)*len(meta))
}
