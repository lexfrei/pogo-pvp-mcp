// Package tools implements the MCP tool handlers (pvp_rank,
// pvp_matchup, …) on top of the engine primitives. Each handler is a
// pure function of its params plus the Manager's current gamemaster.
package tools

import (
	"context"
	"errors"
	"fmt"
	"math"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
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

// Canonical CP caps for the three standard leagues. Overrides from
// RankParams.CPCap take priority.
const (
	greatLeagueCap  = 1500
	ultraLeagueCap  = 2500
	masterLeagueCap = 10000
)

// LeagueCP maps league names to their default CP caps. Exposed so
// callers (the cobra CLI, benchmark harness) can render the same table.
//
//nolint:gochecknoglobals // domain-constant lookup table
var LeagueCP = map[string]int{
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
	League  string `json:"league" jsonschema:"great|ultra|master"`
	CPCap   int    `json:"cp_cap,omitempty" jsonschema:"overrides the league default CP cap"`
	XL      bool   `json:"xl,omitempty" jsonschema:"allow XL candy levels above 40"`
}

// RankResult is the JSON output contract for pvp_rank.
type RankResult struct {
	Species       string  `json:"species"`
	CP            int     `json:"cp"`
	StatProduct   float64 `json:"stat_product"`
	Level         float64 `json:"level"`
	Atk           float64 `json:"atk"`
	Def           float64 `json:"def"`
	HP            int     `json:"hp"`
	PercentOfBest float64 `json:"percent_of_best"`
	League        string  `json:"league"`
	CPCap         int     `json:"cp_cap"`
}

// RankTool wraps the shared gamemaster.Manager and exposes Handler /
// Tool constructors suited to the MCP SDK.
type RankTool struct {
	manager *gamemaster.Manager
}

// NewRankTool constructs a RankTool bound to the given Manager.
func NewRankTool(manager *gamemaster.Manager) *RankTool {
	return &RankTool{manager: manager}
}

// Tool returns the MCP tool registration metadata.
func (rt *RankTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_rank",
		Description: "Rank a Pokémon with given IVs in a PvP league by computing CP, stat product, and percent-of-best for the species.",
	}
}

// Handler returns the MCP-typed handler function.
func (rt *RankTool) Handler() mcp.ToolHandlerFor[RankParams, RankResult] {
	return rt.handle
}

// handle orchestrates the pvp_rank computation: validate inputs, find
// the per-IV level-capped spread, and compare it to the species-global
// optimum to report percent_of_best.
func (rt *RankTool) handle(
	_ context.Context,
	_ *mcp.CallToolRequest,
	params RankParams,
) (*mcp.CallToolResult, RankResult, error) {
	inputs, err := resolveRankInputs(rt.manager, &params)
	if err != nil {
		return nil, RankResult{}, err
	}

	result, err := buildRankResult(inputs)
	if err != nil {
		return nil, RankResult{}, err
	}

	return nil, result, nil
}

// rankInputs bundles the values derived from RankParams that the build
// step needs — keeps handle's body small enough for funlen.
type rankInputs struct {
	species   pogopvp.Species
	ivs       pogopvp.IV
	cpCap     int
	league    string
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

	return RankResult{
		Species:       inputs.speciesID,
		CP:            spread.CP,
		StatProduct:   spread.StatProduct,
		Level:         spread.Level,
		Atk:           stats.Atk,
		Def:           stats.Def,
		HP:            stats.HP,
		PercentOfBest: spread.StatProduct / best.StatProduct * 100,
		League:        inputs.league,
		CPCap:         inputs.cpCap,
	}, nil
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
