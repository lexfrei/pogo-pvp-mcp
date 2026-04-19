package tools

import (
	"context"
	"fmt"
	"math"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// cpLimitLeagues is the ordered list of leagues reported by
// pvp_cp_limits. Master has no CP cap (10000 is unreachable in
// practice) so it is sliced off the shared standardLeagues table.
//
//nolint:gochecknoglobals // ordered domain lookup — struct-literal, no reassignment
var cpLimitLeagues = standardLeagues[:len(standardLeagues)-1]

// CPLimitsParams is the JSON input contract for pvp_cp_limits.
// Shadow variants are addressed by setting Options.Shadow=true; the
// legacy "medicham_shadow" suffix is still tolerated. Lucky /
// Purified accepted for Options-struct symmetry but no-op here (CP
// math is stat-driven; shadow stats are identical to base in the
// pvpoke gamemaster, so Options.Shadow only affects which entry is
// looked up, not the CP number).
type CPLimitsParams struct {
	Species string           `json:"species" jsonschema:"species id in the pvpoke gamemaster"`
	IV      [3]int           `json:"iv" jsonschema:"individual values in [atk, def, sta] order, each 0..15"`
	XL      bool             `json:"xl,omitempty" jsonschema:"allow XL candy levels above 40 — matches pvp_rank's XL flag semantics"`
	Options CombatantOptions `json:"options,omitzero" jsonschema:"shadow / lucky / purified flags; only Shadow is load-bearing here"`
}

// LeagueCPLimit reports the best level + CP a Pokémon with given IVs
// can reach inside one league's CP cap. Fits=false signals the level-1
// baseline CP already exceeds the cap — MaxCP then carries that
// over-cap level-1 CP so the caller sees how far out of range the
// species is.
type LeagueCPLimit struct {
	League   string  `json:"league"`
	CPCap    int     `json:"cp_cap"`
	Fits     bool    `json:"fits"`
	MaxCP    int     `json:"max_cp"`
	MaxLevel float64 `json:"max_level"`
}

// CPLimitsResult is the JSON output contract for pvp_cp_limits. XL
// echoes the request flag so callers can distinguish "level 40, no XL
// candy" from "level 40, XL allowed but nothing above 40 fit" without
// keeping the request context around. ResolvedSpeciesID /
// ShadowVariantMissing echo the shadow-aware lookup.
type CPLimitsResult struct {
	Species              string          `json:"species"`
	ResolvedSpeciesID    string          `json:"resolved_species_id,omitempty"`
	IV                   [3]int          `json:"iv"`
	XL                   bool            `json:"xl"`
	Leagues              []LeagueCPLimit `json:"leagues"`
	ShadowVariantMissing bool            `json:"shadow_variant_missing,omitempty"`
}

// CPLimitsTool wraps the shared gamemaster.Manager and exposes
// Handler / Tool constructors.
type CPLimitsTool struct {
	manager *gamemaster.Manager
}

// NewCPLimitsTool constructs a CPLimitsTool bound to the given Manager.
func NewCPLimitsTool(manager *gamemaster.Manager) *CPLimitsTool {
	return &CPLimitsTool{manager: manager}
}

// cpLimitsToolDescription keeps the Tool struct literal within lll.
const cpLimitsToolDescription = "Return the highest level and CP a Pokémon with given IVs can reach " +
	"while staying under each PvP league's CP cap (Little 500, Great 1500, Ultra 2500). " +
	"Walks the 0.5 level grid up to level 40 by default, or level 51 when xl=true."

// Tool returns the MCP tool registration.
func (tool *CPLimitsTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_cp_limits",
		Description: cpLimitsToolDescription,
	}
}

// Handler returns the MCP-typed handler function.
func (tool *CPLimitsTool) Handler() mcp.ToolHandlerFor[CPLimitsParams, CPLimitsResult] {
	return tool.handle
}

// handle orchestrates the per-league CP-limit calculation. For each
// of Little / Great / Ultra it walks the level grid from MaxLevel
// downward and returns the first level whose CP fits under the cap.
func (tool *CPLimitsTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params CPLimitsParams,
) (*mcp.CallToolResult, CPLimitsResult, error) {
	err := ctx.Err()
	if err != nil {
		return nil, CPLimitsResult{}, fmt.Errorf("cp_limits cancelled: %w", err)
	}

	snapshot := tool.manager.Current()
	if snapshot == nil {
		return nil, CPLimitsResult{}, ErrGamemasterNotLoaded
	}

	species, resolvedID, shadowMissing, ok := resolveSpeciesLookup(snapshot, params.Species, params.Options)
	if !ok {
		return nil, CPLimitsResult{}, fmt.Errorf("%w: %q", ErrUnknownSpecies, params.Species)
	}

	ivs, err := pogopvp.NewIV(params.IV[0], params.IV[1], params.IV[2])
	if err != nil {
		return nil, CPLimitsResult{}, fmt.Errorf("invalid IV: %w", err)
	}

	maxLevel := pogopvp.NoXLMaxLevel
	if params.XL {
		maxLevel = pogopvp.MaxLevel
	}

	leagues := make([]LeagueCPLimit, len(cpLimitLeagues))
	for i, league := range cpLimitLeagues {
		leagues[i] = computeLeagueLimit(species.BaseStats, ivs, league.Name, league.Cap, maxLevel)
	}

	return nil, CPLimitsResult{
		Species:              params.Species,
		ResolvedSpeciesID:    resolvedID,
		IV:                   params.IV,
		XL:                   params.XL,
		Leagues:              leagues,
		ShadowVariantMissing: shadowMissing,
	}, nil
}

// computeLeagueLimit walks the 0.5 level grid downward from maxLevel
// and returns the first level whose CP fits under the cap. maxLevel is
// supplied by the caller so the XL flag can flip between
// pogopvp.NoXLMaxLevel (40) and pogopvp.MaxLevel (51) without
// duplicating the loop. If even level 1 exceeds the cap, Fits=false
// and MaxCP/MaxLevel describe the level-1 baseline so callers see how
// far over the species is.
func computeLeagueLimit(
	base pogopvp.BaseStats, ivs pogopvp.IV, leagueName string, cpCap int, maxLevel float64,
) LeagueCPLimit {
	out := LeagueCPLimit{League: leagueName, CPCap: cpCap}

	doubledMax := int(math.Round(maxLevel * 2))
	doubledMin := int(math.Round(pogopvp.MinLevel * 2))

	for doubled := doubledMax; doubled >= doubledMin; doubled-- {
		level := float64(doubled) / 2

		cpm, err := pogopvp.CPMAt(level)
		if err != nil {
			continue
		}

		combatPower := pogopvp.ComputeCP(base, ivs, cpm)
		if combatPower > cpCap {
			continue
		}

		out.Fits = true
		out.MaxCP = combatPower
		out.MaxLevel = level

		return out
	}

	// Unreachable in practice given the pogopvp.ComputeCP floor of 10
	// and the 500-CP Little Cup floor, but keeps the function total so
	// a future cap lowered below the CP floor still returns a sensible
	// level-1 baseline instead of a zero value.
	cpm, _ := pogopvp.CPMAt(pogopvp.MinLevel)
	out.MaxCP = pogopvp.ComputeCP(base, ivs, cpm)
	out.MaxLevel = pogopvp.MinLevel

	return out
}
