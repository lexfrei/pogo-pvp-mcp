package tools

import (
	"context"
	"fmt"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SpeciesInfoParams is the JSON input contract for pvp_species_info.
// Shadow variants are addressed by setting Options.Shadow=true; the
// legacy "medicham_shadow" suffix is still tolerated. Shadow rows in
// pvpoke carry their OWN LegacyMoves list and (sometimes) distinct
// rankings, so Options.Shadow is load-bearing. Lucky / Purified are
// accepted for Options-struct symmetry but no-op here.
type SpeciesInfoParams struct {
	Species string           `json:"species" jsonschema:"species id in the pvpoke gamemaster"`
	Options CombatantOptions `json:"options,omitzero" jsonschema:"shadow / lucky / purified flags; only Shadow is load-bearing here"`
}

// SpeciesInfoMoveRef is the per-move projection in SpeciesInfoResult's
// FastMoves / ChargedMoves. Carries the engine Move fields clients
// usually want — power, energy, type, duration — plus Legacy and
// Elite flags scoped to the parent species (both categories are
// per-species, not per-move).
type SpeciesInfoMoveRef struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Power      int    `json:"power"`
	Energy     int    `json:"energy,omitempty"`
	EnergyGain int    `json:"energy_gain,omitempty"`
	Cooldown   int    `json:"cooldown,omitempty"`
	Turns      int    `json:"turns,omitempty"`
	Legacy     bool   `json:"legacy"`
	Elite      bool   `json:"elite"`
}

// SpeciesLeagueRank reports the species' overall rank in one of the
// standard leagues. Omitted when the species is not present in the
// league's rankings or the rankings fetch failed (best-effort lookup).
type SpeciesLeagueRank struct {
	League string `json:"league"`
	Rank   int    `json:"rank"`
	Rating int    `json:"rating"`
}

// SpeciesInfoResult is the JSON output for pvp_species_info. Species
// echoes params.Species verbatim (the caller's input id).
// ResolvedSpeciesID is the pvpoke gamemaster key actually used —
// differs from Species only when Options.Shadow=true flipped to the
// "_shadow" row. Name / Dex / BaseStats / FastMoves / ChargedMoves /
// LegacyMoves / Evolutions / PreEvolution / Tags / Released all
// come from the resolved row. ShadowVariantMissing=true signals
// that Options.Shadow was set but pvpoke has no dedicated shadow
// entry — the content then comes from the base species.
//
// Naming convention across Phase X-II tools: Species = verbatim
// input echo, ResolvedSpeciesID = pvpoke key used. The two
// coincide for non-shadow requests (no redirect) — the field is
// still emitted in that case so the response shape is uniform.
type SpeciesInfoResult struct {
	Species              string               `json:"species"`
	ResolvedSpeciesID    string               `json:"resolved_species_id,omitempty"`
	Name                 string               `json:"name"`
	Dex                  int                  `json:"dex"`
	Types                []string             `json:"types"`
	BaseStats            SpeciesBaseStats     `json:"base_stats"`
	FastMoves            []SpeciesInfoMoveRef `json:"fast_moves"`
	ChargedMoves         []SpeciesInfoMoveRef `json:"charged_moves"`
	LegacyMoves          []string             `json:"legacy_moves"`
	EliteMoves           []string             `json:"elite_moves"`
	Evolutions           []string             `json:"evolutions"`
	PreEvolution         string               `json:"pre_evolution,omitempty"`
	Tags                 []string             `json:"tags"`
	Released             bool                 `json:"released"`
	LeagueRanks          []SpeciesLeagueRank  `json:"league_ranks,omitempty"`
	ShadowVariantMissing bool                 `json:"shadow_variant_missing,omitempty"`
}

// SpeciesBaseStats mirrors pogopvp.BaseStats with JSON-friendly tags.
type SpeciesBaseStats struct {
	Atk int `json:"atk"`
	Def int `json:"def"`
	HP  int `json:"hp"`
}

// SpeciesInfoTool is a read-only lookup over gamemaster + rankings.
type SpeciesInfoTool struct {
	gm       *gamemaster.Manager
	rankings *rankings.Manager
}

// NewSpeciesInfoTool constructs the tool. ranks may be nil in tests
// that don't care about league_ranks — the response just omits the
// field in that case.
func NewSpeciesInfoTool(gm *gamemaster.Manager, ranks *rankings.Manager) *SpeciesInfoTool {
	return &SpeciesInfoTool{gm: gm, rankings: ranks}
}

const speciesInfoToolDescription = "Look up a species in the current gamemaster: types, base stats, " +
	"full move lists (with per-move legacy flag), evolution chain, tags, and its rank in each " +
	"standard league."

// Tool returns the MCP registration.
func (tool *SpeciesInfoTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_species_info",
		Description: speciesInfoToolDescription,
	}
}

// Handler returns the MCP-typed handler.
func (tool *SpeciesInfoTool) Handler() mcp.ToolHandlerFor[SpeciesInfoParams, SpeciesInfoResult] {
	return tool.handle
}

// nonNilStrings normalises a possibly-nil string slice to a
// guaranteed non-nil empty slice so JSON marshalling emits `[]`
// rather than `null`. Matches the wire-shape invariant established
// by legacyReverseIndex / projectSpeciesMoves / chargedMoveIDs.
func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}

	return in
}

// handle orchestrates the lookup. Any rankings fetch failures are
// tolerated silently — league_ranks is a best-effort summary.
func (tool *SpeciesInfoTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params SpeciesInfoParams,
) (*mcp.CallToolResult, SpeciesInfoResult, error) {
	err := ctx.Err()
	if err != nil {
		return nil, SpeciesInfoResult{}, fmt.Errorf("species_info cancelled: %w", err)
	}

	snapshot := tool.gm.Current()
	if snapshot == nil {
		return nil, SpeciesInfoResult{}, ErrGamemasterNotLoaded
	}

	species, resolvedID, shadowMissing, ok := resolveSpeciesLookup(snapshot, params.Species, params.Options)
	if !ok {
		return nil, SpeciesInfoResult{}, fmt.Errorf("%w: %q", ErrUnknownSpecies, params.Species)
	}

	result := SpeciesInfoResult{
		Species:              params.Species,
		ResolvedSpeciesID:    resolvedID,
		Name:                 species.Name,
		Dex:                  species.Dex,
		Types:                nonNilStrings(species.Types),
		BaseStats:            SpeciesBaseStats{Atk: species.BaseStats.Atk, Def: species.BaseStats.Def, HP: species.BaseStats.HP},
		FastMoves:            projectSpeciesMoves(snapshot, &species, species.FastMoves),
		ChargedMoves:         projectSpeciesMoves(snapshot, &species, species.ChargedMoves),
		LegacyMoves:          nonNilStrings(species.LegacyMoves),
		EliteMoves:           nonNilStrings(species.EliteMoves),
		Evolutions:           nonNilStrings(species.Evolutions),
		PreEvolution:         species.PreEvolution,
		Tags:                 nonNilStrings(species.Tags),
		Released:             species.Released,
		LeagueRanks:          lookupLeagueRanks(ctx, tool.rankings, species.ID),
		ShadowVariantMissing: shadowMissing,
	}

	return nil, result, nil
}

// projectSpeciesMoves converts an engine Move id list into the info
// tool's richer shape, skipping unknown ids (defensive — gamemaster
// parse already drops known-bad moves like TRANSFORM so this is
// belt-and-braces).
func projectSpeciesMoves(
	snapshot *pogopvp.Gamemaster, species *pogopvp.Species, moveIDs []string,
) []SpeciesInfoMoveRef {
	out := make([]SpeciesInfoMoveRef, 0, len(moveIDs))

	for _, id := range moveIDs {
		move, ok := snapshot.Moves[id]
		if !ok {
			continue
		}

		out = append(out, SpeciesInfoMoveRef{
			ID:         move.ID,
			Type:       move.Type,
			Power:      move.Power,
			Energy:     move.Energy,
			EnergyGain: move.EnergyGain,
			Cooldown:   move.Cooldown,
			Turns:      move.Turns,
			Legacy:     pogopvp.IsLegacyMove(species, move.ID),
			Elite:      pogopvp.IsEliteMove(species, move.ID),
		})
	}

	return out
}

// lookupLeagueRanks returns the species' overall rank across the
// four standard leagues. A nil rankings manager, a 404, or a species
// absent from the ranking produces no entry — this is a best-effort
// summary, not a hard requirement. Errors are swallowed on purpose.
func lookupLeagueRanks(
	ctx context.Context, ranks *rankings.Manager, speciesID string,
) []SpeciesLeagueRank {
	if ranks == nil {
		return nil
	}

	var out []SpeciesLeagueRank

	for _, league := range standardLeagues {
		entries, err := ranks.Get(ctx, league.Cap, "")
		if err != nil {
			continue
		}

		for i := range entries {
			if entries[i].SpeciesID != speciesID {
				continue
			}

			out = append(out, SpeciesLeagueRank{
				League: league.Name,
				Rank:   i + 1,
				Rating: entries[i].Rating,
			})

			break
		}
	}

	return out
}
