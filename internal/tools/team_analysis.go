package tools

import (
	"context"
	"errors"
	"fmt"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrTeamSizeMismatch is returned when the team slice does not contain
// exactly [TeamSize] combatants.
var ErrTeamSizeMismatch = errors.New("team must have exactly 3 members")

// TeamSize is the fixed number of combatants in a PvP team.
const TeamSize = 3

// defaultTeamTopN is how many meta species the analysis sweeps when
// the caller does not pick a value.
const defaultTeamTopN = 30

// defaultShieldsPerSide is the shield count used on both sides when
// the caller does not override.
const defaultShieldsPerSide = 1

// hardWinRating is the battle-rating threshold above which a matchup
// counts as a "hard win" for the member (most of the opponent's HP
// gone, most of our HP left).
const hardWinRating = 750

// hardLossRating mirrors hardWinRating on the losing side.
const hardLossRating = 250

// ratingMidpoint is the tie midpoint for battle_rating (0..1000).
const ratingMidpoint = 500

// uncoveredThreshold is the best-of-team rating below which a meta
// species is flagged as an uncovered threat.
const uncoveredThreshold = 400

// TeamAnalysisParams is the JSON input contract for pvp_team_analysis.
type TeamAnalysisParams struct {
	Team    []Combatant `json:"team" jsonschema:"exactly 3 team members"`
	League  string      `json:"league" jsonschema:"great|ultra|master"`
	TopN    int         `json:"top_n,omitempty" jsonschema:"how many meta species to sweep (default 30)"`
	Shields [2]int      `json:"shields,omitempty" jsonschema:"[team, meta] shields per match; defaults [1, 1]"`
}

// TeamMemberAnalysis describes one team member's performance against
// the sampled meta.
type TeamMemberAnalysis struct {
	Species    string   `json:"species"`
	AvgRating  float64  `json:"avg_rating"`
	Wins       int      `json:"wins"`
	Losses     int      `json:"losses"`
	Ties       int      `json:"ties"`
	HardWins   []string `json:"hard_wins"`
	HardLosses []string `json:"hard_losses"`
}

// TeamAnalysisResult is the JSON output contract for pvp_team_analysis.
type TeamAnalysisResult struct {
	League    string               `json:"league"`
	CPCap     int                  `json:"cp_cap"`
	MetaSize  int                  `json:"meta_size"`
	TeamScore float64              `json:"team_score"`
	PerMember []TeamMemberAnalysis `json:"per_member"`
	Coverage  map[string]int       `json:"coverage_matrix"`
	Uncovered []string             `json:"uncovered_threats"`
}

// TeamAnalysisTool wraps the gamemaster and rankings managers.
type TeamAnalysisTool struct {
	gm       *gamemaster.Manager
	rankings *rankings.Manager
}

// NewTeamAnalysisTool constructs the tool bound to the given managers.
func NewTeamAnalysisTool(gm *gamemaster.Manager, ranks *rankings.Manager) *TeamAnalysisTool {
	return &TeamAnalysisTool{gm: gm, rankings: ranks}
}

// teamAnalysisDescription keeps the Tool struct literal under the line
// length limit.
const teamAnalysisDescription = "Analyse a 3-member PvP team against the current top-N meta: " +
	"per-member average battle rating, hard wins/losses, meta coverage matrix, and uncovered threats."

// Tool returns the MCP tool registration.
func (tool *TeamAnalysisTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_team_analysis",
		Description: teamAnalysisDescription,
	}
}

// Handler returns the MCP-typed handler.
func (tool *TeamAnalysisTool) Handler() mcp.ToolHandlerFor[TeamAnalysisParams, TeamAnalysisResult] {
	return tool.handle
}

// handle orchestrates the analysis: resolve the user team into engine
// combatants, resolve the meta top-N, simulate every (member × meta)
// pair, and aggregate.
func (tool *TeamAnalysisTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params TeamAnalysisParams,
) (*mcp.CallToolResult, TeamAnalysisResult, error) {
	err := ctx.Err()
	if err != nil {
		return nil, TeamAnalysisResult{}, fmt.Errorf("team_analysis cancelled: %w", err)
	}

	if len(params.Team) != TeamSize {
		return nil, TeamAnalysisResult{}, fmt.Errorf("%w: got %d", ErrTeamSizeMismatch, len(params.Team))
	}

	snapshot := tool.gm.Current()
	if snapshot == nil {
		return nil, TeamAnalysisResult{}, ErrGamemasterNotLoaded
	}

	cpCap, err := resolveCPCap(params.League, 0)
	if err != nil {
		return nil, TeamAnalysisResult{}, err
	}

	defaults := resolveTeamDefaults(&params)

	entries, err := tool.rankings.Get(ctx, cpCap)
	if err != nil {
		return nil, TeamAnalysisResult{}, fmt.Errorf("rankings fetch: %w", err)
	}

	topN := min(defaults.TopN, len(entries))
	metaEntries := entries[:topN]

	metaCombatants, err := buildMetaCombatants(snapshot, metaEntries, cpCap, defaults.MetaShields)
	if err != nil {
		return nil, TeamAnalysisResult{}, err
	}

	teamCombatants, err := buildTeamCombatants(snapshot, params.Team, defaults.TeamShields)
	if err != nil {
		return nil, TeamAnalysisResult{}, err
	}

	return nil, runTeamAnalysis(teamCombatants, metaCombatants, metaEntries, params.League, cpCap, topN), nil
}

// teamAnalysisDefaults bundles the three values resolveTeamDefaults
// applies when the caller leaves them zeroed.
type teamAnalysisDefaults struct {
	TopN        int
	TeamShields int
	MetaShields int
}

// resolveTeamDefaults applies the defaults for TopN and shields.
func resolveTeamDefaults(params *TeamAnalysisParams) teamAnalysisDefaults {
	out := teamAnalysisDefaults{
		TopN:        params.TopN,
		TeamShields: params.Shields[0],
		MetaShields: params.Shields[1],
	}

	if out.TopN == 0 {
		out.TopN = defaultTeamTopN
	}

	if out.TeamShields == 0 {
		out.TeamShields = defaultShieldsPerSide
	}

	if out.MetaShields == 0 {
		out.MetaShields = defaultShieldsPerSide
	}

	return out
}

// buildTeamCombatants resolves the user-provided Combatant specs into
// engine combatants via the shared buildEngineCombatant helper from
// matchup.go.
func buildTeamCombatants(
	snapshot *pogopvp.Gamemaster, specs []Combatant, shields int,
) ([]pogopvp.Combatant, error) {
	out := make([]pogopvp.Combatant, len(specs))

	for i := range specs {
		combatant, err := buildEngineCombatant(snapshot, &specs[i], shields)
		if err != nil {
			return nil, fmt.Errorf("team member %d: %w", i, err)
		}

		out[i] = combatant
	}

	return out, nil
}

// buildMetaCombatants converts ranking entries into engine combatants
// by locating the species in the gamemaster, running the IV finder to
// get the optimal spread under the CP cap, and using the ranking's
// recommended moveset (first element fast, remainder charged).
func buildMetaCombatants(
	snapshot *pogopvp.Gamemaster, entries []rankings.RankingEntry, cpCap, shields int,
) ([]pogopvp.Combatant, error) {
	out := make([]pogopvp.Combatant, 0, len(entries))

	for i := range entries {
		combatant, err := buildOneMetaCombatant(snapshot, &entries[i], cpCap, shields)
		if err != nil {
			return nil, fmt.Errorf("meta entry %d (%s): %w", i, entries[i].SpeciesID, err)
		}

		out = append(out, combatant)
	}

	return out, nil
}

// buildOneMetaCombatant is the per-entry helper for buildMetaCombatants.
func buildOneMetaCombatant(
	snapshot *pogopvp.Gamemaster, entry *rankings.RankingEntry, cpCap, shields int,
) (pogopvp.Combatant, error) {
	species, ok := snapshot.Pokemon[entry.SpeciesID]
	if !ok {
		return pogopvp.Combatant{}, fmt.Errorf("%w: %q", ErrUnknownSpecies, entry.SpeciesID)
	}

	if len(entry.Moveset) < 2 {
		return pogopvp.Combatant{}, fmt.Errorf(
			"%w: moveset has %d entries, need at least 2 (fast + 1 charged)",
			ErrUnknownMove, len(entry.Moveset))
	}

	spread, err := pogopvp.FindOptimalSpread(species.BaseStats, cpCap, pogopvp.FindSpreadOpts{
		XLAllowed:   true,
		MaxLevelCap: pogopvp.NoXLMaxLevel, // pvpoke default ranking level cap is 40
	})
	if err != nil {
		return pogopvp.Combatant{}, fmt.Errorf("find optimal spread: %w", err)
	}

	fast, ok := snapshot.Moves[entry.Moveset[0]]
	if !ok {
		return pogopvp.Combatant{}, fmt.Errorf("%w: fast %q", ErrUnknownMove, entry.Moveset[0])
	}

	charged, err := resolveChargedMoves(snapshot, entry.Moveset[1:])
	if err != nil {
		return pogopvp.Combatant{}, err
	}

	return pogopvp.Combatant{
		Species:      species,
		IV:           spread.IV,
		Level:        spread.Level,
		FastMove:     fast,
		ChargedMoves: charged,
		Shields:      shields,
	}, nil
}

// resolveChargedMoves looks up each id in the gamemaster moves map and
// returns the corresponding slice.
func resolveChargedMoves(snapshot *pogopvp.Gamemaster, ids []string) ([]pogopvp.Move, error) {
	out := make([]pogopvp.Move, 0, len(ids))

	for _, id := range ids {
		move, ok := snapshot.Moves[id]
		if !ok {
			return nil, fmt.Errorf("%w: charged %q", ErrUnknownMove, id)
		}

		out = append(out, move)
	}

	return out, nil
}

// runTeamAnalysis simulates the full cartesian product of user team ×
// meta, aggregates ratings, and builds the output struct.
func runTeamAnalysis(
	team, meta []pogopvp.Combatant,
	metaEntries []rankings.RankingEntry,
	league string, cpCap, topN int,
) TeamAnalysisResult {
	perMember := make([]TeamMemberAnalysis, len(team))
	coverage := make(map[string]int, len(meta))
	bestRatings := make(map[string]int, len(meta))

	var overallSum float64

	for memberIdx := range team {
		perMember[memberIdx] = simulateMemberVsMeta(&team[memberIdx], meta, metaEntries)

		for oppIdx := range meta {
			rating := ratingFor(&team[memberIdx], &meta[oppIdx])
			overallSum += float64(rating)

			opp := metaEntries[oppIdx].SpeciesID
			if rating > bestRatings[opp] {
				bestRatings[opp] = rating
				coverage[opp] = rating
			}
		}
	}

	uncovered := findUncoveredThreats(bestRatings, metaEntries)

	denom := len(team) * len(meta)

	var teamScore float64
	if denom > 0 {
		teamScore = overallSum / float64(denom)
	}

	return TeamAnalysisResult{
		League:    league,
		CPCap:     cpCap,
		MetaSize:  topN,
		TeamScore: teamScore,
		PerMember: perMember,
		Coverage:  coverage,
		Uncovered: uncovered,
	}
}

// simulateMemberVsMeta returns the aggregate rating stats for one team
// member against the full meta slice.
func simulateMemberVsMeta(
	member *pogopvp.Combatant,
	meta []pogopvp.Combatant,
	metaEntries []rankings.RankingEntry,
) TeamMemberAnalysis {
	analysis := TeamMemberAnalysis{
		Species: member.Species.ID,
	}

	var sum float64

	for i := range meta {
		rating := ratingFor(member, &meta[i])
		sum += float64(rating)

		opponent := metaEntries[i].SpeciesID

		switch {
		case rating > hardWinRating:
			analysis.HardWins = append(analysis.HardWins, opponent)
		case rating < hardLossRating:
			analysis.HardLosses = append(analysis.HardLosses, opponent)
		}

		switch {
		case rating > ratingMidpoint:
			analysis.Wins++
		case rating < ratingMidpoint:
			analysis.Losses++
		default:
			analysis.Ties++
		}
	}

	if len(meta) > 0 {
		analysis.AvgRating = sum / float64(len(meta))
	}

	return analysis
}

// findUncoveredThreats returns meta species whose best-of-team rating
// stays below uncoveredThreshold.
func findUncoveredThreats(
	bestRatings map[string]int, metaEntries []rankings.RankingEntry,
) []string {
	var out []string

	for i := range metaEntries {
		id := metaEntries[i].SpeciesID
		if bestRatings[id] < uncoveredThreshold {
			out = append(out, id)
		}
	}

	return out
}

// ratingFor returns the 0..1000 battle rating from the attacker's POV
// for a single match. 500 is a tie/timeout midpoint; every HP point
// above zero on either side nudges the rating toward the winner.
func ratingFor(attacker, defender *pogopvp.Combatant) int {
	result, err := pogopvp.Simulate(attacker, defender, pogopvp.BattleOptions{})
	if err != nil {
		return ratingMidpoint
	}

	attMax := initialHP(attacker)
	defMax := initialHP(defender)

	switch result.Winner {
	case 0:
		return ratingMidpoint + scaleHP(result.HPRemaining[0], attMax)
	case 1:
		return ratingMidpoint - scaleHP(result.HPRemaining[1], defMax)
	default:
		return ratingMidpoint
	}
}

// initialHP returns the combatant's starting HP after CPM flooring so
// ratingFor can normalise remaining HP to a 0..1 range.
func initialHP(combatant *pogopvp.Combatant) int {
	cpm, err := pogopvp.CPMAt(combatant.Level)
	if err != nil {
		return 1
	}

	return pogopvp.ComputeStats(combatant.Species.BaseStats, combatant.IV, cpm).HP
}

// scaleHP maps an absolute HP value in [0, maxHP] to a 0..500 range.
// Max is clamped to 1 so the mapping never divides by zero.
func scaleHP(hp, maxHP int) int {
	if maxHP <= 0 {
		return 0
	}

	return ratingMidpoint * hp / maxHP
}
