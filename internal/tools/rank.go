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
	"sort"

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

// leagueSpec pairs a league's display name with its CP cap. Shared
// across species_info / cplimits / evolution_preview so the league
// ordering is declared once.
type leagueSpec struct {
	Name string
	Cap  int
}

// standardLeagues is the canonical ordered list of the four standard
// PvP leagues in ascending CP-cap order. Consumers that omit the
// master league (pvp_cp_limits) slice off the tail.
//
//nolint:gochecknoglobals // ordered domain table, no reassignment
var standardLeagues = []leagueSpec{
	{"little", littleLeagueCap},
	{"great", greatLeagueCap},
	{"ultra", ultraLeagueCap},
	{"master", masterLeagueCap},
}

// RankParams is the JSON input contract for the pvp_rank tool. Shadow
// and purified forms are not exposed yet — the engine carries a Form
// enum but the tool pipeline treats every Pokémon as regular until the
// shadow atk/def multipliers are applied. The parameter will reappear
// once that wiring is in place so callers get a different CP / SP when
// they ask for shadow.
type RankParams struct {
	Species string           `json:"species" jsonschema:"species id in the pvpoke gamemaster (e.g. \"medicham\")"`
	IV      [3]int           `json:"iv" jsonschema:"individual values in [atk, def, sta] order, each 0..15"`
	League  string           `json:"league" jsonschema:"little|great|ultra|master"`
	CPCap   int              `json:"cp_cap,omitempty" jsonschema:"overrides the league default CP cap"`
	XL      bool             `json:"xl,omitempty" jsonschema:"allow XL candy levels above 40"`
	Options CombatantOptions `json:"options,omitzero" jsonschema:"shadow / lucky / purified flags; Shadow flips to the pvpoke _shadow entry"`
}

// CupRanking is one entry in RankResult.RankingsByCup — the species'
// position inside one pvpoke-published cup ranking for the requested
// league. Cup IDs observed in the current gamemaster include "all"
// (open league), "spring", "retro", "jungle", etc. Only cups where
// the species actually appears in pvpoke's per-cup ranking list are
// emitted; cups where the species is filtered out or ranks below
// pvpoke's cutoff are dropped so the array stays signal-dense.
//
// Moveset carries pvpoke's cup-specific recommended build (legacy
// flag included); per-cup NonLegacyMoveset is NOT computed yet —
// full meta re-simulation per cup would multiply the rank call's
// cost by the cup count. Top-level NonLegacyMoveset remains
// available as a best-effort open-league alternative.
type CupRanking struct {
	Cup     string   `json:"cup"`
	Rank    int      `json:"rank"`
	Rating  int      `json:"rating"`
	Moveset *Moveset `json:"moveset,omitempty"`
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
//
// Top-level OptimalMoveset / NonLegacyMoveset / Hundo are projected
// from pvpoke's open-league ("all") rankings — the open-league
// optimal build is the reference baseline. RankingsByCup carries
// the species' position inside every additional pvpoke per-cup
// ranking for the requested league (spring / retro / jungle /
// etc.); each entry is a CupRanking with rank, rating, and
// cup-specific moveset. Cups where the species does not appear in
// pvpoke's per-cup list (filtered out by cup rules, or below the
// cutoff) are dropped from the array.
//
// NonLegacyMoveset (top-level) is populated only when
// OptimalMoveset.HasLegacy=true — for species whose pvpoke-
// recommended build contains a CD / event / ETM-only move, callers
// see the best alternative without legacy plus the rating delta so
// the trade-off is explicit. Per-cup NonLegacyMoveset is not
// emitted yet (each cup would require a full non-legacy meta
// re-simulation, multiplying pvp_rank latency by the cup count).
type RankResult struct {
	Species              string            `json:"species"`
	ResolvedSpeciesID    string            `json:"resolved_species_id,omitempty"`
	CP                   int               `json:"cp"`
	StatProduct          float64           `json:"stat_product"`
	Level                float64           `json:"level"`
	Atk                  float64           `json:"atk"`
	Def                  float64           `json:"def"`
	HP                   int               `json:"hp"`
	PercentOfBest        float64           `json:"percent_of_best"`
	League               string            `json:"league"`
	CPCap                int               `json:"cp_cap"`
	OptimalMoveset       *Moveset          `json:"optimal_moveset,omitempty"`
	NonLegacyMoveset     *NonLegacyMoveset `json:"non_legacy_moveset,omitempty"`
	Hundo                *HundoComparison  `json:"comparison_to_hundo,omitempty"`
	RankingsByCup        []CupRanking      `json:"rankings_by_cup,omitempty"`
	ShadowVariantMissing bool              `json:"shadow_variant_missing,omitempty"`
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

	result.OptimalMoveset = tool.lookupMoveset(ctx, inputs.cpCap, "", &inputs.species)
	result.NonLegacyMoveset = tool.nonLegacyAlternative(ctx, &inputs, result.OptimalMoveset)
	result.RankingsByCup = tool.buildRankingsByCup(ctx, &inputs)

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

// buildRankingsByCup fetches every pvpoke per-cup ranking for the
// requested league cap and returns the species' entry per cup
// (rank / rating / moveset). The "all" (open-league) cup is
// always attempted first; then every named cup published in the
// gamemaster is tried. No LevelCap-based filtering — that field
// is the Pokémon level cap (40 / 50), not the CP cap, so any
// such filter silently dropped real cups like `little` at CPCap=500.
// Cups where the species is not present in pvpoke's per-cup
// ranking slice are dropped — the array stays signal-dense.
//
// The rankings manager caches per-(cap, cup) fetches — both
// positive and negative (404) results — so the O(N) Get calls
// across all cups are cheap after the first warm-up. Unknown /
// missing cup rankings surface as a silently-skipped entry. The
// ctx.Err() check at the loop boundary honours the project-wide
// invariant that handlers release on client disconnect mid-sweep.
func (tool *RankTool) buildRankingsByCup(
	ctx context.Context, inputs *rankInputs,
) []CupRanking {
	if tool.rankings == nil {
		return nil
	}

	snapshot := tool.manager.Current()
	if snapshot == nil {
		return nil
	}

	cupIDs := cupIDsForLookup(snapshot)

	out := make([]CupRanking, 0, len(cupIDs))

	for _, cupID := range cupIDs {
		if ctx.Err() != nil {
			return out
		}

		entry := lookupCupRanking(ctx, tool.rankings, inputs.cpCap, cupID, &inputs.species)
		if entry == nil {
			continue
		}

		out = append(out, *entry)
	}

	return out
}

// openLeagueCupID is pvpoke's conventional id for the open-league
// ranking slice. The cacheKey code in rankings.Manager maps empty
// cup names to this literal; cupIDsForLookup skips both spellings
// to avoid emitting a duplicate open-league entry alongside the
// implicit leading "" we already prepend.
const openLeagueCupID = "all"

// cupIDsForLookup returns the list of pvpoke cup ids to try: "" for
// the open-league slice first, then every named cup from the
// gamemaster sorted alphabetically. No filtering on the cup's
// LevelCap — that field is the Pokémon level cap (40 for Classic,
// 50 for Equinox/Little, 0 for "inherit"), not the CP cap, so
// comparing it against the league CP cap is never meaningful and
// silently dropped real cups like `little` at cpCap=500. Unsupported
// (cup, cap) pairs silently fall out later: rankings.Manager.Get
// returns ErrUnknownCup on upstream 404, and lookupCupRanking
// discards a nil result without adding to the output array.
func cupIDsForLookup(snapshot *pogopvp.Gamemaster) []string {
	names := make([]string, 0, len(snapshot.Cups))

	for cupID := range snapshot.Cups {
		if cupID == "" || cupID == openLeagueCupID {
			continue
		}

		names = append(names, cupID)
	}

	sort.Strings(names)

	out := make([]string, 0, 1+len(names))
	out = append(out, "")
	out = append(out, names...)

	return out
}

// lookupCupRanking fetches one (cap, cup) ranking and projects the
// species' entry into a CupRanking. Returns nil when the rankings
// fetch fails or the species is not in the list; this keeps
// buildRankingsByCup's outer loop straightforward.
func lookupCupRanking(
	ctx context.Context, ranks *rankings.Manager,
	cpCap int, cupID string, species *pogopvp.Species,
) *CupRanking {
	entries, err := ranks.Get(ctx, cpCap, cupID)
	if err != nil {
		return nil
	}

	for i := range entries {
		if entries[i].SpeciesID != species.ID {
			continue
		}

		return &CupRanking{
			Cup:     resolveCupLabel(cupID),
			Rank:    i + 1,
			Rating:  entries[i].Rating,
			Moveset: movesetFromEntry(species, &entries[i]),
		}
	}

	return nil
}

// movesetFromEntry projects a rankings entry's Moveset slice into
// the tool's Moveset shape with per-move legacy tagging. Returns
// nil when the entry carries no moveset data so the caller can
// omit the field.
func movesetFromEntry(species *pogopvp.Species, entry *rankings.RankingEntry) *Moveset {
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

// rankInputs bundles the values derived from RankParams that the build
// step needs — keeps handle's body small enough for funlen. speciesID
// echoes params.Species verbatim (the caller's input id); resolvedID
// is the pvpoke gamemaster key actually used for the lookup (same as
// input unless Options.Shadow=true flipped to the "_shadow" entry).
// shadowVariantMissing=true when Options.Shadow was set but pvpoke
// published no dedicated shadow entry — falls back to the base
// species with the flag raised so the response can signal the
// approximation.
type rankInputs struct {
	species              pogopvp.Species
	ivs                  pogopvp.IV
	cpCap                int
	league               string
	opts                 pogopvp.FindSpreadOpts
	speciesID            string
	resolvedID           string
	shadowVariantMissing bool
}

// resolveRankInputs performs all upfront validation and IV / league
// resolution so [buildRankResult] can focus on math.
func resolveRankInputs(manager *gamemaster.Manager, params *RankParams) (rankInputs, error) {
	gm := manager.Current()
	if gm == nil {
		return rankInputs{}, ErrGamemasterNotLoaded
	}

	species, resolvedID, shadowMissing, ok := resolveSpeciesLookup(gm, params.Species, params.Options)
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
		species:              species,
		ivs:                  ivs,
		cpCap:                cpCap,
		league:               params.League,
		opts:                 pogopvp.FindSpreadOpts{XLAllowed: params.XL},
		speciesID:            params.Species,
		resolvedID:           resolvedID,
		shadowVariantMissing: shadowMissing,
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
		Species:              inputs.speciesID,
		ResolvedSpeciesID:    inputs.resolvedID,
		CP:                   spread.CP,
		StatProduct:          spread.StatProduct,
		Level:                spread.Level,
		Atk:                  stats.Atk,
		Def:                  stats.Def,
		HP:                   stats.HP,
		PercentOfBest:        percentOfBest,
		League:               inputs.league,
		CPCap:                inputs.cpCap,
		Hundo:                computeHundo(inputs),
		ShadowVariantMissing: inputs.shadowVariantMissing,
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
	entries, err := tool.rankings.Get(ctx, inputs.cpCap, "")
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
