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

// ThreatCoverageParams is the JSON input for pvp_threat_coverage.
// The team + pool must both resolve under the given league + cup.
// Shields is the symmetric scenarios list (empty → [1]); ratings
// are averaged across scenarios the way team_analysis does.
type ThreatCoverageParams struct {
	Team           []Combatant `json:"team" jsonschema:"exactly 3 team members whose coverage we are auditing"`
	CandidatePool  []Combatant `json:"candidate_pool" jsonschema:"pool of potential replacement / rotation options to screen"`
	League         string      `json:"league" jsonschema:"little|great|ultra|master"`
	Cup            string      `json:"cup,omitempty" jsonschema:"cup id from pvpoke; empty = open-league all"`
	TopN           int         `json:"top_n,omitempty" jsonschema:"meta size to sweep (default 30)"`
	Shields        []int       `json:"shields,omitempty" jsonschema:"symmetric shield scenarios (default [1]); each 0..2"`
	DisallowLegacy bool        `json:"disallow_legacy,omitempty" jsonschema:"reject legacy moves on team or pool inputs"`
}

// ThreatCandidateMatchup is one pool member's scored result against a
// specific uncovered threat: the counter's resolved moveset and its
// averaged battle rating vs that threat.
type ThreatCandidateMatchup struct {
	Counter      ResolvedCombatant `json:"counter"`
	BattleRating int               `json:"battle_rating"`
}

// ThreatCoverageEntry groups one uncovered meta threat with the pool
// members that cover it. CandidatesThatCover is sorted by rating
// descending and limited to at most defaultThreatCoverageCandidates
// so the output stays tractable.
type ThreatCoverageEntry struct {
	Threat              string                   `json:"threat"`
	TeamBestRating      int                      `json:"team_best_rating"`
	CandidatesThatCover []ThreatCandidateMatchup `json:"candidates_that_cover"`
}

// ThreatCoverageResult is the JSON output for pvp_threat_coverage.
// SkippedMetaSpecies mirrors team_analysis: meta entries whose
// species / moves were not found in the current gamemaster snapshot
// (typical for a post-balance-patch cache race between gamemaster
// and rankings) are dropped from the sweep and listed here so the
// caller can tell "not covered" from "never simulated".
type ThreatCoverageResult struct {
	League             string                `json:"league"`
	Cup                string                `json:"cup"`
	CPCap              int                   `json:"cp_cap"`
	MetaSize           int                   `json:"meta_size"`
	Scenarios          []int                 `json:"scenarios"`
	Team               []ResolvedCombatant   `json:"team"`
	TeamCoverage       map[string]int        `json:"team_coverage_matrix"`
	UncoveredThreats   []ThreatCoverageEntry `json:"uncovered_threats"`
	SkippedMetaSpecies []string              `json:"skipped_meta_species,omitempty"`
}

// ThreatCoverageTool wraps the gamemaster + rankings managers.
type ThreatCoverageTool struct {
	gm       *gamemaster.Manager
	rankings *rankings.Manager
}

// NewThreatCoverageTool constructs the tool bound to the managers.
func NewThreatCoverageTool(gm *gamemaster.Manager, ranks *rankings.Manager) *ThreatCoverageTool {
	return &ThreatCoverageTool{gm: gm, rankings: ranks}
}

const threatCoverageToolDescription = "Identify meta threats the 3-member team does not cover (best-of-team " +
	"battle rating < 400/1000, averaged across shield scenarios), and for each such threat surface up to 3 " +
	"candidate_pool members whose own averaged rating clears the same threshold, sorted by rating descending."

// defaultThreatCoverageCandidates caps how many covering candidates
// are reported per uncovered threat — enough to give the caller
// options without exploding the response on a large pool.
const defaultThreatCoverageCandidates = 3

// Tool returns the MCP tool registration.
func (tool *ThreatCoverageTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_threat_coverage",
		Description: threatCoverageToolDescription,
	}
}

// Handler returns the MCP-typed handler.
func (tool *ThreatCoverageTool) Handler() mcp.ToolHandlerFor[ThreatCoverageParams, ThreatCoverageResult] {
	return tool.handle
}

// threatCoverageWorkspace bundles resolved state between the prepare
// and compute phases of handle. SkippedMeta captures meta entries
// dropped by buildMetaCombatants so the handler can surface them
// instead of letting the zero-rating conflate "team loses" with
// "never simulated".
type threatCoverageWorkspace struct {
	teamCombatants []pogopvp.Combatant
	teamSpecs      []Combatant
	poolCombatants []pogopvp.Combatant
	poolSpecs      []Combatant
	metaCombatants []pogopvp.Combatant
	metaEntries    []rankings.RankingEntry
	skippedMeta    []string
	scenarios      []int
	cpCap          int
}

// handle orchestrates the threat-coverage computation: validate,
// resolve team / pool / meta, compute team coverage, filter the
// uncovered set, and for each uncovered threat rank pool members by
// their rating. Ctx cancellation is polled between outer loops.
func (tool *ThreatCoverageTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params ThreatCoverageParams,
) (*mcp.CallToolResult, ThreatCoverageResult, error) {
	err := validateThreatCoverageParams(ctx, &params)
	if err != nil {
		return nil, ThreatCoverageResult{}, err
	}

	workspace, err := tool.prepareThreatCoverage(ctx, &params)
	if err != nil {
		return nil, ThreatCoverageResult{}, err
	}

	teamCoverage, err := computeTeamCoverage(ctx, workspace)
	if err != nil {
		return nil, ThreatCoverageResult{}, err
	}

	uncovered := buildUncoveredEntries(ctx, workspace, teamCoverage)

	if ctx.Err() != nil {
		return nil, ThreatCoverageResult{}, fmt.Errorf("threat_coverage cancelled: %w", ctx.Err())
	}

	teamResolved := make([]ResolvedCombatant, len(workspace.teamSpecs))
	for i := range workspace.teamSpecs {
		teamResolved[i] = resolvedFromSpec(&workspace.teamSpecs[i])
	}

	return nil, ThreatCoverageResult{
		League:             params.League,
		Cup:                resolveCupLabel(params.Cup),
		CPCap:              workspace.cpCap,
		MetaSize:           len(workspace.metaEntries),
		Scenarios:          workspace.scenarios,
		Team:               teamResolved,
		TeamCoverage:       teamCoverage,
		UncoveredThreats:   uncovered,
		SkippedMetaSpecies: workspace.skippedMeta,
	}, nil
}

// computeTeamCoverage returns the map of per-meta-species best
// rating across the team, averaged over the scenarios slice.
// Extracted from handle so funlen stays under budget and the double
// loop is independently testable.
func computeTeamCoverage(
	ctx context.Context, workspace *threatCoverageWorkspace,
) (map[string]int, error) {
	teamCoverage := make(map[string]int, len(workspace.metaCombatants))

	for teamIdx := range workspace.teamCombatants {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("threat_coverage cancelled: %w", ctx.Err())
		}

		for metaIdx := range workspace.metaCombatants {
			rating, ok := averageRatingAcrossScenarios(
				&workspace.teamCombatants[teamIdx],
				&workspace.metaCombatants[metaIdx],
				workspace.scenarios)
			if !ok {
				continue
			}

			opp := workspace.metaEntries[metaIdx].SpeciesID
			if rating > teamCoverage[opp] {
				teamCoverage[opp] = rating
			}
		}
	}

	return teamCoverage, nil
}

// validateThreatCoverageParams runs cheap pre-flight checks before
// any gamemaster / rankings work.
func validateThreatCoverageParams(ctx context.Context, params *ThreatCoverageParams) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("threat_coverage cancelled: %w", err)
	}

	if len(params.Team) != TeamSize {
		return fmt.Errorf("%w: got %d members, want %d",
			ErrTeamSizeMismatch, len(params.Team), TeamSize)
	}

	if len(params.CandidatePool) > MaxPoolSize {
		return fmt.Errorf("%w: pool size %d exceeds MaxPoolSize %d",
			ErrPoolTooLarge, len(params.CandidatePool), MaxPoolSize)
	}

	if params.TopN < 0 {
		return fmt.Errorf("%w: top_n %d must be non-negative",
			ErrInvalidTopN, params.TopN)
	}

	return validateShields(params.Shields)
}

// prepareThreatCoverage resolves gamemaster snapshot, CP cap,
// scenarios, team + pool movesets (auto-filling when empty), and
// the meta top-N. Shares the team-side helpers with team_analysis.
func (tool *ThreatCoverageTool) prepareThreatCoverage(
	ctx context.Context, params *ThreatCoverageParams,
) (*threatCoverageWorkspace, error) {
	snapshot := tool.gm.Current()
	if snapshot == nil {
		return nil, ErrGamemasterNotLoaded
	}

	cpCap, err := resolveCPCap(params.League, 0)
	if err != nil {
		return nil, err
	}

	scenarios := resolveTeamDefaults(params.Shields, 0).Scenarios
	shields := scenarios[0]

	teamCombatants, err := tool.resolveCombatantSlice(
		ctx, snapshot, params.Team, "team", cpCap, shields, params.Cup, params.DisallowLegacy)
	if err != nil {
		return nil, err
	}

	poolCombatants, err := tool.resolveCombatantSlice(
		ctx, snapshot, params.CandidatePool, "candidate_pool",
		cpCap, shields, params.Cup, params.DisallowLegacy)
	if err != nil {
		return nil, err
	}

	metaCombatants, metaEntries, skippedMeta, err := resolveMeta(
		ctx, tool.rankings, snapshot, cpCap, params.Cup, params.TopN, shields)
	if err != nil {
		return nil, err
	}

	return &threatCoverageWorkspace{
		teamCombatants: teamCombatants,
		teamSpecs:      params.Team,
		poolCombatants: poolCombatants,
		poolSpecs:      params.CandidatePool,
		metaCombatants: metaCombatants,
		metaEntries:    metaEntries,
		skippedMeta:    skippedMeta,
		scenarios:      scenarios,
		cpCap:          cpCap,
	}, nil
}

// resolveCombatantSlice runs the legacy gate + per-member moveset
// defaulting + combatant construction over a slice (team or pool).
// Factored out so prepareThreatCoverage stays under gocyclo.
func (tool *ThreatCoverageTool) resolveCombatantSlice(
	ctx context.Context, snapshot *pogopvp.Gamemaster,
	specs []Combatant, label string,
	cpCap, shields int, cup string, disallowLegacy bool,
) ([]pogopvp.Combatant, error) {
	err := rejectTeamLegacy(snapshot, specs, disallowLegacy)
	if err != nil {
		return nil, err
	}

	for i := range specs {
		err = applyMovesetDefaults(ctx, tool.rankings, &specs[i], cpCap, cup,
			snapshot, disallowLegacy)
		if err != nil {
			return nil, fmt.Errorf("%s[%d] moveset: %w", label, i, err)
		}
	}

	return buildTeamCombatants(snapshot, specs, shields)
}

// resolveMeta fetches the top-N meta rankings and materialises them
// into engine combatants under the given CP cap. Returns the live
// combatant + entry slices plus the list of species ids that
// buildMetaCombatants dropped (gamemaster/rankings cache skew) so
// the caller can surface them in the response. The quadruple return
// is (combatants, kept entries, skipped species ids, error).
func resolveMeta(
	ctx context.Context, ranks *rankings.Manager, snapshot *pogopvp.Gamemaster,
	cpCap int, cup string, topN, shields int,
) ([]pogopvp.Combatant, []rankings.RankingEntry, []string, error) {
	metaTopN := topN
	if metaTopN == 0 {
		metaTopN = defaultTeamTopN
	}

	entries, err := ranks.Get(ctx, cpCap, cup)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("rankings fetch: %w", err)
	}

	entries = entries[:min(metaTopN, len(entries))]

	combatants, kept, skipped, err := buildMetaCombatants(snapshot, entries, cpCap, shields)
	if err != nil {
		return nil, nil, nil, err
	}

	return combatants, kept, skipped, nil
}

// buildUncoveredEntries filters the meta species whose team coverage
// is below uncoveredThreshold and, for each one, scores the pool
// members against it. Each uncovered entry carries up to
// defaultThreatCoverageCandidates pool members (sorted by rating
// desc) whose rating is at or above uncoveredThreshold.
func buildUncoveredEntries(
	ctx context.Context,
	workspace *threatCoverageWorkspace,
	teamCoverage map[string]int,
) []ThreatCoverageEntry {
	out := make([]ThreatCoverageEntry, 0)

	for metaIdx := range workspace.metaCombatants {
		if ctx.Err() != nil {
			return out
		}

		threat := workspace.metaEntries[metaIdx].SpeciesID

		best := teamCoverage[threat]
		if best >= uncoveredThreshold {
			continue
		}

		matchups := scorePoolAgainstThreat(
			workspace.poolCombatants, workspace.poolSpecs,
			&workspace.metaCombatants[metaIdx], workspace.scenarios)

		sort.SliceStable(matchups, func(i, j int) bool {
			return matchups[i].BattleRating > matchups[j].BattleRating
		})

		matchups = trimCoveringCandidates(matchups)

		out = append(out, ThreatCoverageEntry{
			Threat:              threat,
			TeamBestRating:      best,
			CandidatesThatCover: matchups,
		})
	}

	return out
}

// scorePoolAgainstThreat returns one ThreatCandidateMatchup per pool
// member whose averaged rating vs the threat is at or above
// uncoveredThreshold. Sub-threshold ratings are not surfaced as
// candidates — "covers" is defined identically to the team-coverage
// test (flip side of the same threshold).
func scorePoolAgainstThreat(
	pool []pogopvp.Combatant, poolSpecs []Combatant,
	threat *pogopvp.Combatant, scenarios []int,
) []ThreatCandidateMatchup {
	out := make([]ThreatCandidateMatchup, 0, len(pool))

	for i := range pool {
		rating, ok := averageRatingAcrossScenarios(&pool[i], threat, scenarios)
		if !ok {
			continue
		}

		if rating < uncoveredThreshold {
			continue
		}

		out = append(out, ThreatCandidateMatchup{
			Counter:      resolvedFromSpec(&poolSpecs[i]),
			BattleRating: rating,
		})
	}

	return out
}

// trimCoveringCandidates caps the candidate list length at
// defaultThreatCoverageCandidates — callers get the top N by rating.
func trimCoveringCandidates(matchups []ThreatCandidateMatchup) []ThreatCandidateMatchup {
	if len(matchups) <= defaultThreatCoverageCandidates {
		return matchups
	}

	return matchups[:defaultThreatCoverageCandidates]
}
