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

// ErrInvalidShields is returned when the shields slice is neither nil
// nor of length 2 with values in [0, MaxShields].
var ErrInvalidShields = errors.New("invalid shields")

// ErrMovesetTooShort is returned when a rankings entry for a meta
// species carries fewer than 2 moveset slots (fast + ≥1 charged).
// Distinct from ErrUnknownMove so callers debugging the
// SkippedMetaSpecies list can tell a malformed-rankings race apart
// from a real gamemaster/rankings id mismatch.
var ErrMovesetTooShort = errors.New("moveset too short")

// TeamSize is the fixed number of combatants in a PvP team.
const TeamSize = 3

// maxShieldCount mirrors pogopvp.MaxShields without a direct import
// dependency; kept in sync with the engine constant.
const maxShieldCount = 2

// validateShields accepts an optional list of shield-scenario counts.
// Each entry must be in [0, maxShieldCount]; every scenario runs with
// that shield count on both sides. Empty / nil is treated as the
// default single-scenario [1].
func validateShields(shields []int) error {
	if len(shields) == 0 {
		return nil
	}

	for i, value := range shields {
		if value < 0 || value > maxShieldCount {
			return fmt.Errorf("%w: shields[%d]=%d outside [0, %d]",
				ErrInvalidShields, i, value, maxShieldCount)
		}
	}

	return nil
}

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

// rankingsMaxLevelCap mirrors the levelCap pvpoke uses when generating
// the per-league rankings JSONs we ingest (50 in the XL-candy era).
// Meta-combatants are resolved under this cap so their spreads match
// what pvpoke simulated when producing the rankings.
const rankingsMaxLevelCap = 50

// TeamAnalysisParams is the JSON input contract for pvp_team_analysis.
// Shields is a list of symmetric shield scenarios: each entry forces
// both sides to that count; ratings are averaged across scenarios.
// nil / empty → [1] (single 1v1 scenario). Phase E broke the v0.1
// `[team, meta]` asymmetric pair — pre-v0.1 rename.
type TeamAnalysisParams struct {
	Team           []Combatant `json:"team" jsonschema:"exactly 3 team members"`
	League         string      `json:"league" jsonschema:"little|great|ultra|master"`
	Cup            string      `json:"cup,omitempty" jsonschema:"cup id from pvpoke; empty = open-league all"`
	TopN           int         `json:"top_n,omitempty" jsonschema:"meta species to sweep (default 30)"`
	Shields        []int       `json:"shields,omitempty" jsonschema:"symmetric shield scenarios; omit for [1]; averaged; each 0..2"`
	DisallowLegacy bool        `json:"disallow_legacy,omitempty" jsonschema:"reject legacy moves; default false (legacy allowed)"`
}

// TeamMemberAnalysis describes one team member's performance against
// the sampled meta. FastMove and ChargedMoves echo the resolved
// moveset (either the caller's explicit choice or the recommended
// default from rankings) so the client can see what was simulated.
type TeamMemberAnalysis struct {
	Species      string   `json:"species"`
	FastMove     string   `json:"fast_move"`
	ChargedMoves []string `json:"charged_moves"`
	AvgRating    float64  `json:"avg_rating"`
	Wins         int      `json:"wins"`
	Losses       int      `json:"losses"`
	Ties         int      `json:"ties"`
	HardWins     []string `json:"hard_wins"`
	HardLosses   []string `json:"hard_losses"`
}

// TeamAnalysisResult is the JSON output contract for pvp_team_analysis.
// SimulationFailures counts (member, meta) pairs whose engine call
// returned an error and were skipped for aggregation purposes; a
// non-zero value means the team score is less trustworthy.
// SkippedMetaSpecies lists meta entries whose species / moves were
// not found in the current gamemaster snapshot and were therefore
// dropped from the simulation (typical for a post-balance-patch
// cache race between gamemaster and rankings).
type TeamAnalysisResult struct {
	League             string               `json:"league"`
	Cup                string               `json:"cup"`
	CPCap              int                  `json:"cp_cap"`
	MetaSize           int                  `json:"meta_size"`
	TeamScore          float64              `json:"team_score"`
	SimulationFailures int                  `json:"simulation_failures"`
	SkippedMetaSpecies []string             `json:"skipped_meta_species,omitempty"`
	PerMember          []TeamMemberAnalysis `json:"per_member"`
	Coverage           map[string]int       `json:"coverage_matrix"`
	Uncovered          []string             `json:"uncovered_threats"`
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

// teamAnalysisWorkspace is the fully-resolved state the simulation
// phase consumes. Building it happens in prepareTeamAnalysis; the
// handler then just runs the simulation and labels the result.
// Scenarios is the list of symmetric shield counts to simulate per
// (member, opponent) pair; their ratings are averaged.
type teamAnalysisWorkspace struct {
	TeamCombatants []pogopvp.Combatant
	MetaCombatants []pogopvp.Combatant
	KeptEntries    []rankings.RankingEntry
	SkippedMeta    []string
	CPCap          int
	Scenarios      []int
}

// handle orchestrates the analysis: resolve the user team into engine
// combatants, resolve the meta top-N, simulate every (member × meta)
// pair, and aggregate.
func (tool *TeamAnalysisTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params TeamAnalysisParams,
) (*mcp.CallToolResult, TeamAnalysisResult, error) {
	err := validateTeamAnalysisParams(ctx, &params)
	if err != nil {
		return nil, TeamAnalysisResult{}, err
	}

	workspace, err := tool.prepareTeamAnalysis(ctx, &params)
	if err != nil {
		return nil, TeamAnalysisResult{}, err
	}

	result := runTeamAnalysis(ctx, workspace.TeamCombatants, workspace.MetaCombatants,
		workspace.KeptEntries, params.League, workspace.CPCap,
		len(workspace.KeptEntries), workspace.Scenarios)
	result.Cup = resolveCupLabel(params.Cup)
	result.SkippedMetaSpecies = workspace.SkippedMeta

	if ctx.Err() != nil {
		return nil, TeamAnalysisResult{}, fmt.Errorf("team_analysis cancelled: %w", ctx.Err())
	}

	return nil, result, nil
}

// prepareTeam orchestrates the team-side prep: legacy rejection
// (under DisallowLegacy) plus per-member moveset defaulting plus
// combatant construction. Split off prepareTeamAnalysis so funlen
// stays under budget.
func (tool *TeamAnalysisTool) prepareTeam(
	ctx context.Context, snapshot *pogopvp.Gamemaster,
	params *TeamAnalysisParams, cpCap, shields int,
) ([]pogopvp.Combatant, error) {
	err := rejectTeamLegacy(snapshot, params.Team, params.DisallowLegacy)
	if err != nil {
		return nil, err
	}

	for i := range params.Team {
		err = applyMovesetDefaults(ctx, tool.rankings, &params.Team[i], cpCap, params.Cup,
			snapshot, params.DisallowLegacy)
		if err != nil {
			return nil, fmt.Errorf("team[%d] moveset: %w", i, err)
		}
	}

	return buildTeamCombatants(snapshot, params.Team, shields)
}

// prepareTeamAnalysis resolves all inputs (gamemaster snapshot, CP
// cap, rankings, moveset defaults, combatants) into a workspace for
// the simulation phase. Keeps handle under funlen.
func (tool *TeamAnalysisTool) prepareTeamAnalysis(
	ctx context.Context, params *TeamAnalysisParams,
) (*teamAnalysisWorkspace, error) {
	snapshot := tool.gm.Current()
	if snapshot == nil {
		return nil, ErrGamemasterNotLoaded
	}

	cpCap, err := resolveCPCap(params.League, 0)
	if err != nil {
		return nil, err
	}

	defaults := resolveTeamDefaults(params.Shields, params.TopN)

	entries, err := tool.rankings.Get(ctx, cpCap, params.Cup)
	if err != nil {
		return nil, fmt.Errorf("rankings fetch: %w", err)
	}

	metaEntries := entries[:min(defaults.TopN, len(entries))]

	metaCombatants, keptEntries, skipped, err := buildMetaCombatants(
		snapshot, metaEntries, cpCap, defaults.Scenarios[0])
	if err != nil {
		return nil, err
	}

	teamCombatants, err := tool.prepareTeam(ctx, snapshot, params, cpCap, defaults.Scenarios[0])
	if err != nil {
		return nil, err
	}

	return &teamAnalysisWorkspace{
		TeamCombatants: teamCombatants,
		MetaCombatants: metaCombatants,
		KeptEntries:    keptEntries,
		SkippedMeta:    skipped,
		Scenarios:      defaults.Scenarios,
		CPCap:          cpCap,
	}, nil
}

// validateTeamAnalysisParams runs the cheap pre-flight checks (cancel,
// team size, top_n, shields) before any gamemaster or rankings calls.
func validateTeamAnalysisParams(ctx context.Context, params *TeamAnalysisParams) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("team_analysis cancelled: %w", err)
	}

	if len(params.Team) != TeamSize {
		return fmt.Errorf("%w: got %d", ErrTeamSizeMismatch, len(params.Team))
	}

	if params.TopN < 0 {
		return fmt.Errorf("%w: %d must be non-negative", ErrInvalidTopN, params.TopN)
	}

	return validateShields(params.Shields)
}

// teamAnalysisDefaults bundles the two values resolveTeamDefaults
// applies when the caller leaves them zeroed. Scenarios lists the
// shield counts — each entry simulates both sides at that count;
// ratings are averaged across the list.
type teamAnalysisDefaults struct {
	TopN      int
	Scenarios []int
}

// resolveTeamDefaults applies the defaults for TopN and shields. An
// omitted Shields field (nil / empty slice) falls back to [1] (one
// scenario of 1v1 shields — the v0.1 default). A present slice is
// taken as the list of symmetric shield scenarios to simulate; the
// final rating is the mean across scenarios. Breaking change relative
// to the v0.1 [team, meta] semantics; documented in CLAUDE.md.
func resolveTeamDefaults(shields []int, topN int) teamAnalysisDefaults {
	out := teamAnalysisDefaults{
		TopN:      topN,
		Scenarios: []int{defaultShieldsPerSide},
	}

	if out.TopN == 0 {
		out.TopN = defaultTeamTopN
	}

	if len(shields) > 0 {
		out.Scenarios = append(out.Scenarios[:0], shields...)
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
// recommended moveset (first element fast, remainder charged). Meta
// entries that cannot be resolved (species missing from the gamemaster
// snapshot, malformed moveset, etc.) are skipped — the rankings cache
// and the gamemaster cache refresh on independent 24h cadences and may
// diverge for a day at a time, so one stale entry should not fail the
// whole request. The returned filtered entries slice keeps the caller
// in sync with the combatants slice for downstream indexing.
func buildMetaCombatants(
	snapshot *pogopvp.Gamemaster, entries []rankings.RankingEntry, cpCap, shields int,
) ([]pogopvp.Combatant, []rankings.RankingEntry, []string, error) {
	out := make([]pogopvp.Combatant, 0, len(entries))
	kept := make([]rankings.RankingEntry, 0, len(entries))

	var skipped []string

	for i := range entries {
		combatant, err := buildOneMetaCombatant(snapshot, &entries[i], cpCap, shields)
		if err != nil {
			if errors.Is(err, ErrUnknownSpecies) || errors.Is(err, ErrUnknownMove) ||
				errors.Is(err, ErrMoveCategoryMismatch) ||
				errors.Is(err, ErrMovesetTooShort) {
				skipped = append(skipped, entries[i].SpeciesID)

				continue
			}

			return nil, nil, nil, fmt.Errorf("meta entry %d (%s): %w", i, entries[i].SpeciesID, err)
		}

		out = append(out, combatant)
		kept = append(kept, entries[i])
	}

	return out, kept, skipped, nil
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
			ErrMovesetTooShort, len(entry.Moveset))
	}

	// pvpoke rankings are generated with levelCap=50 (XL-candy era).
	// Mirror that envelope so the meta-combatants we simulate match
	// the spreads pvpoke itself used when computing the rankings.
	spread, err := pogopvp.FindOptimalSpread(species.BaseStats, cpCap, pogopvp.FindSpreadOpts{
		XLAllowed:   true,
		MaxLevelCap: rankingsMaxLevelCap,
	})
	if err != nil {
		return pogopvp.Combatant{}, fmt.Errorf("find optimal spread: %w", err)
	}

	fast, ok := snapshot.Moves[entry.Moveset[0]]
	if !ok {
		return pogopvp.Combatant{}, fmt.Errorf("%w: fast %q", ErrUnknownMove, entry.Moveset[0])
	}

	if fast.Category != pogopvp.MoveCategoryFast {
		return pogopvp.Combatant{}, fmt.Errorf(
			"%w: %q is a charged move but appears in moveset[0]",
			ErrMoveCategoryMismatch, entry.Moveset[0])
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

// resolveChargedMoves looks up each id in the gamemaster moves map,
// verifies its category, and returns the corresponding slice.
func resolveChargedMoves(snapshot *pogopvp.Gamemaster, ids []string) ([]pogopvp.Move, error) {
	out := make([]pogopvp.Move, 0, len(ids))

	for _, moveID := range ids {
		move, ok := snapshot.Moves[moveID]
		if !ok {
			return nil, fmt.Errorf("%w: charged %q", ErrUnknownMove, moveID)
		}

		if move.Category != pogopvp.MoveCategoryCharged {
			return nil, fmt.Errorf(
				"%w: %q is a fast move but appears in charged slot",
				ErrMoveCategoryMismatch, moveID)
		}

		out = append(out, move)
	}

	return out, nil
}

// runTeamAnalysis simulates the full cartesian product of user team ×
// meta, aggregates ratings, and builds the output struct. Each
// (member, meta) pair is simulated exactly once; the rating feeds
// both the per-member stats and the team-wide coverage map. Checks
// ctx.Err() at each outer iteration so a client disconnect aborts
// the sweep.
func runTeamAnalysis(
	ctx context.Context,
	team, meta []pogopvp.Combatant,
	metaEntries []rankings.RankingEntry,
	league string, cpCap, topN int, scenarios []int,
) TeamAnalysisResult {
	perMember := make([]TeamMemberAnalysis, len(team))
	coverage := make(map[string]int, len(meta))

	var (
		overallSum float64
		failures   int
	)

	for memberIdx := range team {
		if ctx.Err() != nil {
			break
		}

		tally := analyzeMember(&team[memberIdx], meta, metaEntries, coverage, scenarios)
		perMember[memberIdx] = tally.Analysis
		overallSum += tally.RatingSum
		failures += tally.Failures
	}

	// Successful matchups count — failures are excluded from both
	// numerator (analyzeMember skipped them) and denominator so the
	// team_score is the average over pairs that actually produced a
	// rating, not depressed toward zero by engine errors.
	successful := len(team)*len(meta) - failures

	var teamScore float64
	if successful > 0 {
		teamScore = overallSum / float64(successful)
	}

	return TeamAnalysisResult{
		League:             league,
		CPCap:              cpCap,
		MetaSize:           topN,
		TeamScore:          teamScore,
		SimulationFailures: failures,
		PerMember:          perMember,
		Coverage:           coverage,
		Uncovered:          findUncoveredThreats(coverage, metaEntries),
	}
}

// memberTally bundles the per-member analysis, the raw sum of ratings
// (for the overall team score), and the count of failed Simulate
// calls so runTeamAnalysis can surface simulation issues rather than
// burying them behind the tie-midpoint fallback.
type memberTally struct {
	Analysis  TeamMemberAnalysis
	RatingSum float64
	Failures  int
}

// analyzeMember simulates one team member against the full meta slice
// and returns its memberTally. The caller-supplied coverage map is
// updated in place with the best rating-per-opponent seen so far.
// Each (member, opponent) pair is simulated once per scenario in
// `scenarios`; the rating used for aggregation is the mean across
// scenarios. A failed scenario skips the rating for that (pair,
// scenario) but does not skip the whole pair.
func analyzeMember(
	member *pogopvp.Combatant,
	meta []pogopvp.Combatant,
	metaEntries []rankings.RankingEntry,
	coverage map[string]int,
	scenarios []int,
) memberTally {
	analysis := TeamMemberAnalysis{
		Species:      member.Species.ID,
		FastMove:     member.FastMove.ID,
		ChargedMoves: chargedMoveIDs(member.ChargedMoves),
	}

	var (
		memberSum float64
		failures  int
	)

	for oppIdx := range meta {
		rating, ok := averageRatingAcrossScenarios(member, &meta[oppIdx], scenarios)
		if !ok {
			failures++

			continue
		}

		memberSum += float64(rating)

		opp := metaEntries[oppIdx].SpeciesID
		tallyMatchup(&analysis, opp, rating)

		if rating > coverage[opp] {
			coverage[opp] = rating
		}
	}

	successful := len(meta) - failures
	if successful > 0 {
		analysis.AvgRating = memberSum / float64(successful)
	}

	return memberTally{Analysis: analysis, RatingSum: memberSum, Failures: failures}
}

// averageRatingAcrossScenarios runs ratingFor with both sides' shield
// counts forced to each scenario value, averages the numeric ratings,
// and reports ok=false if every scenario failed OR the scenarios
// slice is empty. A partial failure (some scenarios errored, others
// produced a rating) still counts as success with the mean of the
// successful scenarios.
//
// An empty scenarios slice returns (0, false) — not a 500 tie — so
// the pair is counted as a failure rather than a silently-injected
// midpoint score. resolveTeamDefaults always supplies at least one
// scenario, so this path is defensive, but the CLAUDE.md invariant
// against 500-midpoint fallbacks must hold regardless of caller
// correctness.
func averageRatingAcrossScenarios(
	member, opponent *pogopvp.Combatant, scenarios []int,
) (int, bool) {
	if len(scenarios) == 0 {
		return 0, false
	}

	var (
		sum     int
		counted int
	)

	for _, shields := range scenarios {
		attacker := *member
		defender := *opponent
		attacker.Shields = shields
		defender.Shields = shields

		rating, err := ratingFor(&attacker, &defender)
		if err != nil {
			continue
		}

		sum += rating
		counted++
	}

	if counted == 0 {
		return 0, false
	}

	return sum / counted, true
}

// tallyMatchup updates a per-member analysis with one matchup rating:
// bumps the win/loss/tie counters and appends to HardWins / HardLosses
// when the rating crosses the threshold.
func tallyMatchup(analysis *TeamMemberAnalysis, opponent string, rating int) {
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
// above zero on either side nudges the rating toward the winner. The
// second return value is the error surface from [pogopvp.Simulate];
// callers (team_analysis / team_builder) use it to count broken
// matchups rather than silently inflating the team score with ties.
func ratingFor(attacker, defender *pogopvp.Combatant) (int, error) {
	result, err := pogopvp.Simulate(attacker, defender, pogopvp.BattleOptions{})
	if err != nil {
		return ratingMidpoint, fmt.Errorf("simulate: %w", err)
	}

	attMax := initialHP(attacker)
	defMax := initialHP(defender)

	switch result.Winner {
	case 0:
		return ratingMidpoint + scaleHP(result.HPRemaining[0], attMax), nil
	case 1:
		return ratingMidpoint - scaleHP(result.HPRemaining[1], defMax), nil
	default:
		return ratingMidpoint, nil
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
// A non-positive maxHP short-circuits to zero so the mapping never
// divides by zero.
func scaleHP(hp, maxHP int) int {
	if maxHP <= 0 {
		return 0
	}

	return ratingMidpoint * hp / maxHP
}
