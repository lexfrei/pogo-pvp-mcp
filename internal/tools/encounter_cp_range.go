package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrUnknownEncounterType is returned when the encounter type name
// is not one of the canonical Pokémon GO sources.
var ErrUnknownEncounterType = errors.New("unknown encounter type")

// EncounterCPRangeParams is the JSON input for pvp_encounter_cp_range.
// Species is required (the tool reports per-species CP ranges); an
// empty EncounterType returns every canonical encounter type.
type EncounterCPRangeParams struct {
	Species       string `json:"species" jsonschema:"species id in the pvpoke gamemaster"`
	EncounterType string `json:"encounter_type,omitempty" jsonschema:"encounter type id (empty = all types)"`
}

// EncounterCPRange is one (encounter type → CP range) row for the
// queried species. MinCP / MaxCP are computed by enumerating the
// allowed (level, IV) combinations under this encounter's rules:
// each encounter type nails down a level (or level range) plus an
// IV floor (e.g. raids lock IVs to 10..15, wild spawns allow 0..15).
type EncounterCPRange struct {
	EncounterType string  `json:"encounter_type"`
	Display       string  `json:"display"`
	MinLevel      float64 `json:"min_level"`
	MaxLevel      float64 `json:"max_level"`
	MinIV         int     `json:"min_iv"`
	MinCP         int     `json:"min_cp"`
	MaxCP         int     `json:"max_cp"`
	Note          string  `json:"note,omitempty"`
}

// EncounterCPRangeResult is the JSON output. Ranges is sorted by
// encounter type name for deterministic ordering.
type EncounterCPRangeResult struct {
	Species string             `json:"species"`
	Query   string             `json:"query,omitempty"`
	Ranges  []EncounterCPRange `json:"ranges"`
}

// EncounterCPRangeTool wraps the gamemaster manager for species
// lookup.
type EncounterCPRangeTool struct {
	gm *gamemaster.Manager
}

// NewEncounterCPRangeTool constructs the tool.
func NewEncounterCPRangeTool(gm *gamemaster.Manager) *EncounterCPRangeTool {
	return &EncounterCPRangeTool{gm: gm}
}

// Canonical Pokémon GO encounter-source level / IV floors. Values
// sourced from Niantic documentation and pvpoke; update when
// Niantic shifts a mechanic (2022 raid rework moved caught-level
// from 20 to 20 with IV floor 10; weather boost still +5 levels).
const (
	encounterWildUnboostedMinLevel = 1
	encounterWildUnboostedMaxLevel = 30
	encounterWildBoostedMinLevel   = 6
	encounterWildBoostedMaxLevel   = 35
	encounterWildBoostedMinIV      = 4
	encounterResearchLevel         = 15
	encounterResearchMinIV         = 10
	encounterRaidLevel             = 20
	encounterRaidMinIV             = 10
	encounterGBLRewardLevel        = 20
	encounterHatchLevel            = 20
	encounterHatchMinIV            = 10
	encounterRocketShadowLevel     = 8
	encounterRocketShadowMinIV     = 1
)

// encounterRule captures the Pokémon GO rules for one encounter
// source: the allowed level window (usually a single pinned level,
// sometimes a wild-spawn range) and the minimum IV component.
type encounterRule struct {
	Type     string
	Display  string
	MinLevel float64
	MaxLevel float64
	MinIV    int
	Note     string
}

// encounterRules is the canonical table of encounter types and
// their level / IV rules. Ordered alphabetically by Type for
// deterministic iteration.
//
//nolint:gochecknoglobals // fixed domain lookup table
var encounterRules = []encounterRule{
	{
		Type: "gbl_reward", Display: "GO Battle League reward encounter",
		MinLevel: encounterGBLRewardLevel, MaxLevel: encounterGBLRewardLevel, MinIV: 0,
		Note: "Pinned at level 20 with standard 0..15 IV range.",
	},
	{
		Type: "hatch_10km", Display: "10km egg hatch",
		MinLevel: encounterHatchLevel, MaxLevel: encounterHatchLevel, MinIV: encounterHatchMinIV,
		Note: "Hatched Pokémon arrive at level 20 with IVs floor-clamped to 10..15.",
	},
	{
		Type: "raid", Display: "Tier 1-5 raid reward",
		MinLevel: encounterRaidLevel, MaxLevel: encounterRaidLevel, MinIV: encounterRaidMinIV,
		Note: "Post-2022 raid rework: caught at level 20 with IV floor 10.",
	},
	{
		Type: "research_15", Display: "Research / field task encounter",
		MinLevel: encounterResearchLevel, MaxLevel: encounterResearchLevel, MinIV: encounterResearchMinIV,
		Note: "Fixed level 15 with IV floor 10. Also covers Research Breakthrough.",
	},
	{
		Type: "rocket_shadow", Display: "Team GO Rocket shadow encounter",
		MinLevel: encounterRocketShadowLevel, MaxLevel: encounterRocketShadowLevel, MinIV: encounterRocketShadowMinIV,
		Note: "Shadow Pokémon are level 8 with IVs floor 1. Purification adds +2 levels and sets IVs to 15/15/15.",
	},
	{
		Type: "wild_boosted", Display: "Wild spawn (weather-boosted)",
		MinLevel: encounterWildBoostedMinLevel, MaxLevel: encounterWildBoostedMaxLevel, MinIV: encounterWildBoostedMinIV,
		Note: "Weather boost adds +5 levels and bumps the IV floor to 4.",
	},
	{
		Type: "wild_unboosted", Display: "Wild spawn (not weather-boosted)",
		MinLevel: encounterWildUnboostedMinLevel, MaxLevel: encounterWildUnboostedMaxLevel, MinIV: 0,
		Note: "Standard wild spawn window. Lures / incense obey the same rule.",
	},
}

const encounterCPRangeToolDescription = "Given a species id, return the min / max CP range it can appear at for " +
	"each canonical Pokémon GO encounter source (wild spawns, research, raids, GBL rewards, egg hatches, Team " +
	"Rocket shadows). Each encounter type encodes its pinned level (or range) and IV floor. Pass " +
	"encounter_type=\"\" for every type; name a specific type for one row."

// Tool returns the MCP tool registration.
func (*EncounterCPRangeTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_encounter_cp_range",
		Description: encounterCPRangeToolDescription,
	}
}

// Handler returns the MCP-typed handler function.
func (tool *EncounterCPRangeTool) Handler() mcp.ToolHandlerFor[EncounterCPRangeParams, EncounterCPRangeResult] {
	return tool.handle
}

// handle validates inputs, looks up the species in the gamemaster,
// then computes min/max CP over the encounter rule's (level, IV)
// space.
func (tool *EncounterCPRangeTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params EncounterCPRangeParams,
) (*mcp.CallToolResult, EncounterCPRangeResult, error) {
	err := ctx.Err()
	if err != nil {
		return nil, EncounterCPRangeResult{}, fmt.Errorf("encounter_cp_range cancelled: %w", err)
	}

	if params.Species == "" {
		return nil, EncounterCPRangeResult{}, fmt.Errorf("%w: species required", ErrUnknownSpecies)
	}

	snapshot := tool.gm.Current()
	if snapshot == nil {
		return nil, EncounterCPRangeResult{}, ErrGamemasterNotLoaded
	}

	species, ok := snapshot.Pokemon[params.Species]
	if !ok {
		return nil, EncounterCPRangeResult{}, fmt.Errorf("%w: %q", ErrUnknownSpecies, params.Species)
	}

	rules, err := pickEncounterRules(params.EncounterType)
	if err != nil {
		return nil, EncounterCPRangeResult{}, err
	}

	ranges := make([]EncounterCPRange, 0, len(rules))
	for _, rule := range rules {
		ranges = append(ranges, computeEncounterRange(&species, rule))
	}

	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].EncounterType < ranges[j].EncounterType
	})

	return nil, EncounterCPRangeResult{
		Species: params.Species,
		Query:   params.EncounterType,
		Ranges:  ranges,
	}, nil
}

// pickEncounterRules returns either the full rule table (empty
// query) or a single-rule slice (named query). Case-insensitive on
// the query.
func pickEncounterRules(query string) ([]encounterRule, error) {
	if query == "" {
		out := make([]encounterRule, len(encounterRules))
		copy(out, encounterRules)

		return out, nil
	}

	normalised := strings.ToLower(query)

	for _, rule := range encounterRules {
		if rule.Type == normalised {
			return []encounterRule{rule}, nil
		}
	}

	return nil, fmt.Errorf("%w: %q (allowed: %s)",
		ErrUnknownEncounterType, query, knownEncounterTypes())
}

// knownEncounterTypes returns the canonical encounter-type names for
// error messages.
func knownEncounterTypes() []string {
	out := make([]string, 0, len(encounterRules))
	for _, rule := range encounterRules {
		out = append(out, rule.Type)
	}

	return out
}

// computeEncounterRange computes the MinCP and MaxCP a species can
// show up with under the given encounter rule. MinCP comes from the
// floor (min level, floor IVs); MaxCP comes from the ceiling (max
// level, 15/15/15 IVs). Stat floors apply the encounter-type's
// MinIV to every component (atk, def, sta) — this matches Niantic's
// documented mechanic for weather-boosted spawns and raids.
func computeEncounterRange(species *pogopvp.Species, rule encounterRule) EncounterCPRange {
	minIV, _ := pogopvp.NewIV(rule.MinIV, rule.MinIV, rule.MinIV)
	maxIV, _ := pogopvp.NewIV(pogopvp.MaxIV, pogopvp.MaxIV, pogopvp.MaxIV)

	var (
		minCP int
		maxCP int
	)

	minCPM, err := pogopvp.CPMAt(rule.MinLevel)
	if err == nil {
		minCP = pogopvp.ComputeCP(species.BaseStats, minIV, minCPM)
	}

	maxCPM, err := pogopvp.CPMAt(rule.MaxLevel)
	if err == nil {
		maxCP = pogopvp.ComputeCP(species.BaseStats, maxIV, maxCPM)
	}

	return EncounterCPRange{
		EncounterType: rule.Type,
		Display:       rule.Display,
		MinLevel:      rule.MinLevel,
		MaxLevel:      rule.MaxLevel,
		MinIV:         rule.MinIV,
		MinCP:         minCP,
		MaxCP:         maxCP,
		Note:          rule.Note,
	}
}
