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
	Cup     string `json:"cup,omitempty" jsonschema:"cup id from pvpoke (spring/retro/etc.); empty=all; affects recommended_moveset only"`
	CPCap   int    `json:"cp_cap,omitempty" jsonschema:"overrides the league default CP cap"`
	XL      bool   `json:"xl,omitempty" jsonschema:"allow XL candy levels above 40"`
}

// Moveset is the fast + charged pairing pvp_rank reports as the
// current recommended build for the species under the requested
// (league, cup). Charged can legitimately be 1- or 2-element
// depending on how pvpoke publishes it — typically 2.
type Moveset struct {
	Fast    string   `json:"fast"`
	Charged []string `json:"charged"`
}

// RankResult is the JSON output contract for pvp_rank. RecommendedMoveset
// is projected from the pvpoke rankings JSON for the requested (cup,
// cap) pair and is absent (omitempty) when either the RankTool was
// constructed without a rankings manager, the rankings fetch failed,
// or the species is not present in the ranking for the requested cup.
type RankResult struct {
	Species            string   `json:"species"`
	CP                 int      `json:"cp"`
	StatProduct        float64  `json:"stat_product"`
	Level              float64  `json:"level"`
	Atk                float64  `json:"atk"`
	Def                float64  `json:"def"`
	HP                 int      `json:"hp"`
	PercentOfBest      float64  `json:"percent_of_best"`
	League             string   `json:"league"`
	Cup                string   `json:"cup"`
	CPCap              int      `json:"cp_cap"`
	RecommendedMoveset *Moveset `json:"recommended_moveset,omitempty"`
}

// RankTool wraps the shared gamemaster.Manager plus an optional
// rankings.Manager. When rankings is non-nil the tool projects the
// species' recommended moveset for the requested (league, cup) into
// RankResult.RecommendedMoveset; a nil rankings reduces the tool to
// its pre-Phase-B behaviour (no moveset in the response).
type RankTool struct {
	manager  *gamemaster.Manager
	rankings *rankings.Manager
}

// NewRankTool constructs a RankTool bound to the given managers.
// ranks may be nil — typically in tests that don't care about
// recommended_moveset — in which case the RecommendedMoveset field
// is always absent from the response.
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

	result.RecommendedMoveset = tool.lookupMoveset(ctx, inputs.cpCap, inputs.cup, inputs.speciesID)

	err = ctx.Err()
	if err != nil {
		return nil, RankResult{}, fmt.Errorf("rank cancelled: %w", err)
	}

	return nil, result, nil
}

// lookupMoveset projects the pvpoke rankings' recommended moveset for
// the species into a Moveset. Best-effort: nil rankings manager, a
// fetch error, a species missing from the ranking slice, or a moveset
// shorter than 1 element all collapse to a nil return so the caller
// just omits the field from the JSON response.
func (tool *RankTool) lookupMoveset(
	ctx context.Context, cpCap int, cup, speciesID string,
) *Moveset {
	if tool.rankings == nil {
		return nil
	}

	entries, err := tool.rankings.Get(ctx, cpCap, cup)
	if err != nil {
		return nil
	}

	for i := range entries {
		entry := entries[i]
		if entry.SpeciesID != speciesID {
			continue
		}

		if len(entry.Moveset) == 0 {
			return nil
		}

		moveset := &Moveset{Fast: entry.Moveset[0]}
		if len(entry.Moveset) > 1 {
			moveset.Charged = append(moveset.Charged, entry.Moveset[1:]...)
		}

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
	}, nil
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
