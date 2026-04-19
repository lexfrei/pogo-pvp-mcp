// Package tools implements the MCP tool handlers exposed by the
// server: pvp_rank, pvp_matchup, pvp_cp_limits, pvp_meta,
// pvp_team_analysis, and pvp_team_builder. Each handler is a pure
// function of its params plus the state pulled from gamemaster.Manager
// and rankings.Manager.
package tools

import (
	"context"
	"errors"
	"fmt"
	"math"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrUnknownSpecies is returned when the requested species id is not
// in the currently loaded gamemaster.
var ErrUnknownSpecies = errors.New("unknown species")

// ErrUnknownLeague is returned when the league string cannot be mapped
// to a CP cap (great / ultra / master).
var ErrUnknownLeague = errors.New("unknown league")

// ErrInvalidCPCap is returned when RankParams.CPCap is negative. A
// typo like cp_cap:-1500 must not silently fall back to the league
// default — failing loud surfaces the mistake at the client.
var ErrInvalidCPCap = errors.New("invalid cp_cap")

// ErrGamemasterNotLoaded is returned when a handler runs before the
// Manager has any data — typically a race at start-up before the first
// Refresh completes.
var ErrGamemasterNotLoaded = errors.New("gamemaster not loaded")

// Canonical CP caps for the four standard leagues. Overrides from
// RankParams.CPCap take priority.
const (
	littleLeagueCap = 500
	greatLeagueCap  = 1500
	ultraLeagueCap  = 2500
	masterLeagueCap = 10000
)

// LeagueCP maps league names to their default CP caps. Exposed so
// callers (the cobra CLI, benchmark harness) can render the same table.
//
//nolint:gochecknoglobals // domain-constant lookup table
var LeagueCP = map[string]int{
	"little": littleLeagueCap,
	"great":  greatLeagueCap,
	"ultra":  ultraLeagueCap,
	"master": masterLeagueCap,
}

// RankParams is the JSON input contract for the pvp_rank tool. Shadow
// and purified forms are not exposed yet — the engine carries a Form
// enum but the tool pipeline treats every Pokémon as regular until the
// shadow atk/def multipliers are applied. The parameter will reappear
// once that wiring is in place so callers get a different CP / SP when
// they ask for shadow.
type RankParams struct {
	Species string `json:"species" jsonschema:"species id in the pvpoke gamemaster (e.g. \"medicham\")"`
	IV      [3]int `json:"iv" jsonschema:"individual values in [atk, def, sta] order, each 0..15"`
	League  string `json:"league" jsonschema:"little|great|ultra|master"`
	Cup     string `json:"cup,omitempty" jsonschema:"cup id from pvpoke (spring/retro/etc.); empty=all; affects optimal_moveset lookup only"`
	CPCap   int    `json:"cp_cap,omitempty" jsonschema:"overrides the league default CP cap"`
	XL      bool   `json:"xl,omitempty" jsonschema:"allow XL candy levels above 40"`
}

// Moveset is the fast + charged pairing pvp_rank reports as the
// current recommended build for the species under the requested
// (league, cup). Charged can legitimately be 1- or 2-element
// depending on how pvpoke publishes it — typically 2. HasLegacy
// flags whether any of Fast / Charged is marked legacy on this
// species; when true, the sibling NonLegacyMoveset on RankResult
// carries the best build available without legacy.
type Moveset struct {
	Fast      string   `json:"fast"`
	Charged   []string `json:"charged"`
	HasLegacy bool     `json:"has_legacy"`
}

// NonLegacyMoveset describes the best-case build for this species
// that avoids every move flagged legacy on the species. Produced by
// full enumeration over non-legacy (fast × up to-2 charged) permutations,
// scored against the top-N meta averaged across shield scenarios.
// RatingDelta is the average-rating difference vs the Moveset carried
// in OptimalMoveset — negative numbers mean the non-legacy build is
// worse (the usual case; legacy moves tend to be CD/event buffs).
// Rationale is a short human-readable note about why the non-legacy
// shape was chosen or is empty (e.g. "species has no non-legacy charged
// moves").
type NonLegacyMoveset struct {
	Fast        string   `json:"fast,omitempty"`
	Charged     []string `json:"charged,omitempty"`
	RatingDelta float64  `json:"rating_delta"`
	Rationale   string   `json:"rationale,omitempty"`
}

// RankResult is the JSON output contract for pvp_rank.
// OptimalMoveset is projected from the pvpoke rankings JSON for the
// requested (cup, cap) pair and is absent when the species is not
// present in the ranking. NonLegacyMoveset is populated only when
// OptimalMoveset.HasLegacy=true — for species whose pvpoke-recommended
// build contains a CD / event / ETM-only move, callers see the best
// alternative without legacy plus the rating delta so the trade-off
// is explicit.
type RankResult struct {
	Species          string            `json:"species"`
	CP               int               `json:"cp"`
	StatProduct      float64           `json:"stat_product"`
	Level            float64           `json:"level"`
	Atk              float64           `json:"atk"`
	Def              float64           `json:"def"`
	HP               int               `json:"hp"`
	PercentOfBest    float64           `json:"percent_of_best"`
	League           string            `json:"league"`
	Cup              string            `json:"cup"`
	CPCap            int               `json:"cp_cap"`
	OptimalMoveset   *Moveset          `json:"optimal_moveset,omitempty"`
	NonLegacyMoveset *NonLegacyMoveset `json:"non_legacy_moveset,omitempty"`
	Hundo            *HundoComparison  `json:"comparison_to_hundo,omitempty"`
}

// HundoComparison carries the best-case 15/15/15 spread for the same
// species under the same CP cap so callers can see how much stat
// product they'd get from a "perfect" catch without a second tool
// call. Omitted under master league / unbounded caps where the
// comparison is uninformative (every IV reaches the same MaxLevel).
type HundoComparison struct {
	Level       float64 `json:"level"`
	CP          int     `json:"cp"`
	StatProduct float64 `json:"stat_product"`
}

// RankTool wraps the shared gamemaster.Manager plus an optional
// rankings.Manager. When rankings is non-nil the tool projects the
// species' pvpoke-recommended build for the requested (league, cup)
// into RankResult.OptimalMoveset (and, when OptimalMoveset.HasLegacy
// is true, searches for a non-legacy alternative in
// RankResult.NonLegacyMoveset); a nil rankings reduces the tool to
// its pre-Phase-B behaviour (no moveset fields in the response).
type RankTool struct {
	manager  *gamemaster.Manager
	rankings *rankings.Manager
}

// NewRankTool constructs a RankTool bound to the given managers.
// ranks may be nil — typically in tests that don't care about
// optimal_moveset / non_legacy_moveset — in which case both
// moveset fields are always absent from the response.
func NewRankTool(manager *gamemaster.Manager, ranks *rankings.Manager) *RankTool {
	return &RankTool{manager: manager, rankings: ranks}
}

// Tool returns the MCP tool registration metadata.
func (tool *RankTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_rank",
		Description: "Rank a Pokémon with given IVs in a PvP league by computing CP, stat product, and percent-of-best for the species.",
	}
}

// Handler returns the MCP-typed handler function.
func (tool *RankTool) Handler() mcp.ToolHandlerFor[RankParams, RankResult] {
	return tool.handle
}

// handle orchestrates the pvp_rank computation: validate inputs, find
// the per-IV level-capped spread, and compare it to the species-global
// optimum to report percent_of_best. Checks context cancellation on
// entry and after the search so a client disconnect stops the worker.
func (tool *RankTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params RankParams,
) (*mcp.CallToolResult, RankResult, error) {
	err := ctx.Err()
	if err != nil {
		return nil, RankResult{}, fmt.Errorf("rank cancelled: %w", err)
	}

	inputs, err := resolveRankInputs(tool.manager, &params)
	if err != nil {
		return nil, RankResult{}, err
	}

	result, err := buildRankResult(inputs)
	if err != nil {
		return nil, RankResult{}, err
	}

	result.OptimalMoveset = tool.lookupMoveset(ctx, inputs.cpCap, inputs.cup, &inputs.species)
	result.NonLegacyMoveset = tool.nonLegacyAlternative(ctx, &inputs, result.OptimalMoveset)

	err = ctx.Err()
	if err != nil {
		return nil, RankResult{}, fmt.Errorf("rank cancelled: %w", err)
	}

	return nil, result, nil
}

// lookupMoveset projects the pvpoke rankings' recommended moveset for
// the species into a Moveset with per-move legacy tagging. Best-effort:
// nil rankings manager, a fetch error, a species missing from the
// ranking slice, or a moveset shorter than 1 element all collapse to
// a nil return so the caller just omits the field from the JSON
// response.
func (tool *RankTool) lookupMoveset(
	ctx context.Context, cpCap int, cup string, species *pogopvp.Species,
) *Moveset {
	if tool.rankings == nil || species == nil {
		return nil
	}

	entries, err := tool.rankings.Get(ctx, cpCap, cup)
	if err != nil {
		return nil
	}

	for i := range entries {
		entry := entries[i]
		if entry.SpeciesID != species.ID {
			continue
		}

		if len(entry.Moveset) == 0 {
			return nil
		}

		moveset := &Moveset{Fast: entry.Moveset[0]}
		if len(entry.Moveset) > 1 {
			moveset.Charged = append(moveset.Charged, entry.Moveset[1:]...)
		}

		moveset.HasLegacy = pogopvp.IsLegacyMove(species, moveset.Fast) ||
			anyLegacyMove(species, moveset.Charged)

		return moveset
	}

	return nil
}

// rankInputs bundles the values derived from RankParams that the build
// step needs — keeps handle's body small enough for funlen.
type rankInputs struct {
	species   pogopvp.Species
	ivs       pogopvp.IV
	cpCap     int
	league    string
	cup       string
	opts      pogopvp.FindSpreadOpts
	speciesID string
}

// resolveRankInputs performs all upfront validation and IV / league
// resolution so [buildRankResult] can focus on math.
func resolveRankInputs(manager *gamemaster.Manager, params *RankParams) (rankInputs, error) {
	gm := manager.Current()
	if gm == nil {
		return rankInputs{}, ErrGamemasterNotLoaded
	}

	species, ok := gm.Pokemon[params.Species]
	if !ok {
		return rankInputs{}, fmt.Errorf("%w: %q", ErrUnknownSpecies, params.Species)
	}

	cpCap, err := resolveCPCap(params.League, params.CPCap)
	if err != nil {
		return rankInputs{}, err
	}

	ivs, err := pogopvp.NewIV(params.IV[0], params.IV[1], params.IV[2])
	if err != nil {
		return rankInputs{}, fmt.Errorf("invalid IV: %w", err)
	}

	return rankInputs{
		species:   species,
		ivs:       ivs,
		cpCap:     cpCap,
		league:    params.League,
		cup:       params.Cup,
		opts:      pogopvp.FindSpreadOpts{XLAllowed: params.XL},
		speciesID: params.Species,
	}, nil
}

// buildRankResult runs the two searches (per-IV and global-best) and
// packages their outputs into a RankResult.
func buildRankResult(inputs rankInputs) (RankResult, error) {
	spread, err := findSpreadForSpecies(inputs.species.BaseStats, inputs.ivs, inputs.cpCap, inputs.opts)
	if err != nil {
		return RankResult{}, err
	}

	best, err := pogopvp.FindOptimalSpread(inputs.species.BaseStats, inputs.cpCap, inputs.opts)
	if err != nil {
		return RankResult{}, fmt.Errorf("best spread: %w", err)
	}

	cpm, err := pogopvp.CPMAt(spread.Level)
	if err != nil {
		return RankResult{}, fmt.Errorf("cpm: %w", err)
	}

	stats := pogopvp.ComputeStats(inputs.species.BaseStats, inputs.ivs, cpm)

	percentOfBest, err := percentOfBest(spread.StatProduct, best.StatProduct)
	if err != nil {
		return RankResult{}, err
	}

	return RankResult{
		Species:       inputs.speciesID,
		CP:            spread.CP,
		StatProduct:   spread.StatProduct,
		Level:         spread.Level,
		Atk:           stats.Atk,
		Def:           stats.Def,
		HP:            stats.HP,
		PercentOfBest: percentOfBest,
		League:        inputs.league,
		Cup:           resolveCupLabel(inputs.cup),
		CPCap:         inputs.cpCap,
		Hundo:         computeHundo(inputs),
	}, nil
}

// computeHundo searches the best 15/15/15 spread under the same cap
// as the requested IV and packages it for the RankResult. Returns
// nil when the resolved cap is at or above masterLeagueCap — every
// spread saturates at MaxLevel there, so the comparison is
// uninformative. The guard keys on the resolved cpCap rather than
// the league string so callers that pass `league:"great",
// cp_cap:10000` still skip the empty comparison.
//
// A search error (e.g. cap so low even hundo can't fit) drops the
// field; that's acceptable because the main spread already surfaced
// its own error above this point.
func computeHundo(inputs rankInputs) *HundoComparison {
	if inputs.cpCap >= masterLeagueCap {
		return nil
	}

	hundoIVs, err := pogopvp.NewIV(pogopvp.MaxIV, pogopvp.MaxIV, pogopvp.MaxIV)
	if err != nil {
		return nil
	}

	spread, err := findSpreadForSpecies(inputs.species.BaseStats, hundoIVs, inputs.cpCap, inputs.opts)
	if err != nil {
		return nil
	}

	return &HundoComparison{
		Level:       spread.Level,
		CP:          spread.CP,
		StatProduct: spread.StatProduct,
	}
}

// nonLegacyScoreScenarios is the shield-scenarios list used by
// nonLegacyAlternative. Single 1-shield scenario matches pvpoke's
// default ranking context and keeps the pvp_rank latency cost
// manageable (extra work only fires when optimal HasLegacy=true).
//
//nolint:gochecknoglobals // domain table, no reassignment
var nonLegacyScoreScenarios = []int{1}

// nonLegacyMetaTopN is how many meta species the non-legacy
// enumeration scores against per candidate moveset. Smaller than
// the main team_analysis top-30 to keep per-request cost bounded.
const nonLegacyMetaTopN = 20

// nonLegacyAlternative returns the best non-legacy moveset for the
// species if the optimal moveset contains at least one legacy move.
// Full enumeration over (non-legacy fast × 1-2 non-legacy charged)
// scored against a top-N meta; RatingDelta is the average-rating
// difference vs the optimal moveset's score against the same meta.
// Returns nil when optimal is nil or has no legacy moves — nothing
// to compare against.
func (tool *RankTool) nonLegacyAlternative(
	ctx context.Context, inputs *rankInputs, optimal *Moveset,
) *NonLegacyMoveset {
	if optimal == nil || !optimal.HasLegacy {
		return nil
	}

	subsets, rationale := partitionNonLegacyMoves(&inputs.species)
	if rationale != "" {
		return &NonLegacyMoveset{Rationale: rationale}
	}

	snapshot := tool.manager.Current()
	if snapshot == nil {
		return nil
	}

	meta, err := tool.nonLegacyMeta(ctx, inputs, snapshot)
	if err != nil || len(meta) == 0 {
		return &NonLegacyMoveset{
			Rationale: "meta unavailable, cannot score non-legacy alternatives",
		}
	}

	spread, err := tool.resolveSpread(inputs)
	if err != nil {
		return nil
	}

	optimalScore := averageMovesetRating(
		snapshot, inputs, spread.Level, optimal.Fast, optimal.Charged, meta)

	best := enumerateNonLegacyMovesets(ctx,
		snapshot, inputs, spread.Level, subsets.fasts, subsets.chargeds, meta)

	if best.fast == "" {
		return &NonLegacyMoveset{Rationale: "no scorable non-legacy combination"}
	}

	return &NonLegacyMoveset{
		Fast:        best.fast,
		Charged:     best.charged,
		RatingDelta: best.score - optimalScore,
	}
}

// nonLegacyMoveSubsets bundles the species' non-legacy fast and
// charged move lists for the enumeration loop. Kept private so the
// helper stays an implementation detail of nonLegacyAlternative.
type nonLegacyMoveSubsets struct {
	fasts    []string
	chargeds []string
}

// partitionNonLegacyMoves returns (subsets, rationale) — rationale
// is non-empty when the species has no viable non-legacy build
// (empty fast or charged subset). An empty rationale signals the
// enumeration should proceed.
//
//nolint:gocritic // unnamedResult: (subsets, rationale) documented on the doc line above
func partitionNonLegacyMoves(species *pogopvp.Species) (nonLegacyMoveSubsets, string) {
	out := nonLegacyMoveSubsets{
		fasts:    nonLegacyMoves(species, species.FastMoves),
		chargeds: nonLegacyMoves(species, species.ChargedMoves),
	}

	if len(out.chargeds) == 0 {
		return out, "species has no non-legacy charged moves"
	}

	if len(out.fasts) == 0 {
		return out, "species has no non-legacy fast moves"
	}

	return out, ""
}

// nonLegacyBest is the winning enumeration candidate passed back
// from enumerateNonLegacyMovesets to the caller. Keeping it a named
// struct instead of a multi-value return placates golangci
// unnamedResult without forcing every call site into named returns.
type nonLegacyBest struct {
	score   float64
	fast    string
	charged []string
}

// nonLegacyMeta fetches and builds the meta combatant slice used
// to score non-legacy alternatives. Kept separate from nonLegacyAlternative
// so the "meta unavailable" branch has a single return path.
func (tool *RankTool) nonLegacyMeta(
	ctx context.Context, inputs *rankInputs, snapshot *pogopvp.Gamemaster,
) ([]pogopvp.Combatant, error) {
	entries, err := tool.rankings.Get(ctx, inputs.cpCap, inputs.cup)
	if err != nil {
		return nil, fmt.Errorf("rankings fetch: %w", err)
	}

	topN := min(nonLegacyMetaTopN, len(entries))
	metaEntries := entries[:topN]

	combatants, _, _, err := buildMetaCombatants(
		snapshot, metaEntries, inputs.cpCap, nonLegacyScoreScenarios[0])
	if err != nil {
		return nil, fmt.Errorf("build meta: %w", err)
	}

	return combatants, nil
}

// resolveSpread re-runs the species-IV level search so
// enumerateNonLegacyMovesets can build fresh combatants without
// replicating buildRankResult's math. CPM is computed implicitly
// by downstream ComputeStats calls; we don't surface it here.
func (tool *RankTool) resolveSpread(
	inputs *rankInputs,
) (pogopvp.OptimalSpread, error) {
	spread, err := findSpreadForSpecies(
		inputs.species.BaseStats, inputs.ivs, inputs.cpCap, inputs.opts)
	if err != nil {
		return pogopvp.OptimalSpread{}, fmt.Errorf("find spread: %w", err)
	}

	return spread, nil
}

// averageMovesetRating builds the species Combatant for the given
// moveset + level and averages its rating vs the meta slice across
// the default shield scenarios. Failures bail with a 0 return; the
// caller compares deltas so an occasional 0 is acceptable noise.
func averageMovesetRating(
	snapshot *pogopvp.Gamemaster, inputs *rankInputs, level float64,
	fastID string, chargedIDs []string, meta []pogopvp.Combatant,
) float64 {
	fast, ok := snapshot.Moves[fastID]
	if !ok || fast.Category != pogopvp.MoveCategoryFast {
		return 0
	}

	charged := make([]pogopvp.Move, 0, len(chargedIDs))

	for _, id := range chargedIDs {
		move, moveOK := snapshot.Moves[id]
		if !moveOK || move.Category != pogopvp.MoveCategoryCharged {
			return 0
		}

		charged = append(charged, move)
	}

	attacker := pogopvp.Combatant{
		Species:      inputs.species,
		IV:           inputs.ivs,
		Level:        level,
		FastMove:     fast,
		ChargedMoves: charged,
		Shields:      nonLegacyScoreScenarios[0],
	}

	var (
		sum     int
		counted int
	)

	for j := range meta {
		rating, rOK := averageRatingAcrossScenarios(&attacker, &meta[j], nonLegacyScoreScenarios)
		if !rOK {
			continue
		}

		sum += rating
		counted++
	}

	if counted == 0 {
		return 0
	}

	return float64(sum) / float64(counted)
}

// enumerateNonLegacyMovesets iterates every (fast × 1-charged) and
// (fast × 2-charged) combination over the non-legacy subsets and
// returns the winning candidate. Deterministic: within ties, the
// first encountered wins. ctx.Err() is polled at the outer loop so
// a client disconnect during a long enumeration releases the
// worker goroutine — matches the CLAUDE.md invariant "ctx.Err() at
// loop boundaries, not just on entry".
func enumerateNonLegacyMovesets(
	ctx context.Context,
	snapshot *pogopvp.Gamemaster, inputs *rankInputs, level float64,
	fasts, chargeds []string, meta []pogopvp.Combatant,
) nonLegacyBest {
	var best nonLegacyBest

	for _, fast := range fasts {
		if ctx.Err() != nil {
			return best
		}

		best = bestPerFast(snapshot, inputs, level, fast, chargeds, meta, best)
	}

	return best
}

// bestPerFast evaluates every 1- and 2-charged combination under
// the given fast move and returns the running champion. Extracted
// from enumerateNonLegacyMovesets to keep the outer loop under
// gocognit budget.
func bestPerFast(
	snapshot *pogopvp.Gamemaster, inputs *rankInputs, level float64,
	fast string, chargeds []string, meta []pogopvp.Combatant,
	best nonLegacyBest,
) nonLegacyBest {
	for i, first := range chargeds {
		single := []string{first}

		singleScore := averageMovesetRating(snapshot, inputs, level, fast, single, meta)
		if best.fast == "" || singleScore > best.score {
			best = nonLegacyBest{score: singleScore, fast: fast, charged: single}
		}

		for j := i + 1; j < len(chargeds); j++ {
			pair := []string{first, chargeds[j]}

			pairScore := averageMovesetRating(snapshot, inputs, level, fast, pair, meta)
			if pairScore > best.score {
				best = nonLegacyBest{score: pairScore, fast: fast, charged: pair}
			}
		}
	}

	return best
}

// ErrDegenerateSpecies is returned when the gamemaster supplies a
// species whose best-possible stat product is zero — division by zero
// would produce NaN / Inf and json.Marshal then fails with a typeless
// error, so the tool surfaces it explicitly instead.
var ErrDegenerateSpecies = errors.New("species has zero global stat product")

// percentOfBest computes spread / best * 100 with a zero-divisor guard.
func percentOfBest(spread, best float64) (float64, error) {
	if best == 0 {
		return 0, ErrDegenerateSpecies
	}

	return spread / best * 100, nil
}

// resolveCPCap returns the override if positive, otherwise the league
// default. Negative overrides are rejected as ErrInvalidCPCap; unknown
// leagues surface ErrUnknownLeague.
func resolveCPCap(league string, override int) (int, error) {
	if override < 0 {
		return 0, fmt.Errorf("%w: %d", ErrInvalidCPCap, override)
	}

	if override > 0 {
		return override, nil
	}

	leagueCap, ok := LeagueCP[league]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownLeague, league)
	}

	return leagueCap, nil
}

// findSpreadForSpecies walks the level grid downward from the configured
// max and returns the highest-level fit for the given IVs under the
// CP cap. It is a narrower helper than FindOptimalSpread (which searches
// all 4096 IVs) because the caller already supplies the IVs.
func findSpreadForSpecies(
	base pogopvp.BaseStats, ivs pogopvp.IV, cpCap int, opts pogopvp.FindSpreadOpts,
) (pogopvp.OptimalSpread, error) {
	maxLevel := pogopvp.NoXLMaxLevel
	if opts.XLAllowed {
		maxLevel = pogopvp.MaxLevel
	}

	doubledMax := int(math.Round(maxLevel * 2))
	doubledMin := int(math.Round(pogopvp.MinLevel * 2))

	for doubled := doubledMax; doubled >= doubledMin; doubled-- {
		level := float64(doubled) / 2

		cpm, err := pogopvp.CPMAt(level)
		if err != nil {
			continue
		}

		if pogopvp.ComputeCP(base, ivs, cpm) > cpCap {
			continue
		}

		stats := pogopvp.ComputeStats(base, ivs, cpm)

		return pogopvp.OptimalSpread{
			IV:          ivs,
			Level:       level,
			CP:          pogopvp.ComputeCP(base, ivs, cpm),
			StatProduct: pogopvp.ComputeStatProduct(stats),
		}, nil
	}

	return pogopvp.OptimalSpread{}, pogopvp.ErrCPCapUnreachable
}
