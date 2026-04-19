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
type SpeciesInfoParams struct {
	Species string `json:"species" jsonschema:"species id in the pvpoke gamemaster (e.g. \"medicham\")"`
}

// SpeciesInfoMoveRef is the per-move projection in SpeciesInfoResult's
// FastMoves / ChargedMoves. Carries the engine Move fields clients
// usually want — power, energy, type, duration — plus the Legacy
// flag scoped to the parent species (legacy is per-species, not
// per-move).
type SpeciesInfoMoveRef struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Power      int    `json:"power"`
	Energy     int    `json:"energy,omitempty"`
	EnergyGain int    `json:"energy_gain,omitempty"`
	Cooldown   int    `json:"cooldown,omitempty"`
	Turns      int    `json:"turns,omitempty"`
	Legacy     bool   `json:"legacy"`
}

// SpeciesLeagueRank reports the species' overall rank in one of the
// standard leagues. Omitted when the species is not present in the
// league's rankings or the rankings fetch failed (best-effort lookup).
type SpeciesLeagueRank struct {
	League string `json:"league"`
	Rank   int    `json:"rank"`
	Rating int    `json:"rating"`
}

// SpeciesInfoResult is the JSON output for pvp_species_info.
type SpeciesInfoResult struct {
	Species      string               `json:"species"`
	Name         string               `json:"name"`
	Dex          int                  `json:"dex"`
	Types        []string             `json:"types"`
	BaseStats    SpeciesBaseStats     `json:"base_stats"`
	FastMoves    []SpeciesInfoMoveRef `json:"fast_moves"`
	ChargedMoves []SpeciesInfoMoveRef `json:"charged_moves"`
	LegacyMoves  []string             `json:"legacy_moves"`
	Evolutions   []string             `json:"evolutions"`
	PreEvolution string               `json:"pre_evolution,omitempty"`
	Tags         []string             `json:"tags"`
	Released     bool                 `json:"released"`
	LeagueRanks  []SpeciesLeagueRank  `json:"league_ranks,omitempty"`
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

// leagueRankLookup lists the leagues pvp_species_info queries.
//
//nolint:gochecknoglobals // fixed domain table
var leagueRankLookup = []struct {
	Name string
	Cap  int
}{
	{"little", littleLeagueCap},
	{"great", greatLeagueCap},
	{"ultra", ultraLeagueCap},
	{"master", masterLeagueCap},
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

	species, ok := snapshot.Pokemon[params.Species]
	if !ok {
		return nil, SpeciesInfoResult{}, fmt.Errorf("%w: %q", ErrUnknownSpecies, params.Species)
	}

	result := SpeciesInfoResult{
		Species:      species.ID,
		Name:         species.Name,
		Dex:          species.Dex,
		Types:        nonNilStrings(species.Types),
		BaseStats:    SpeciesBaseStats{Atk: species.BaseStats.Atk, Def: species.BaseStats.Def, HP: species.BaseStats.HP},
		FastMoves:    projectSpeciesMoves(snapshot, &species, species.FastMoves),
		ChargedMoves: projectSpeciesMoves(snapshot, &species, species.ChargedMoves),
		LegacyMoves:  nonNilStrings(species.LegacyMoves),
		Evolutions:   nonNilStrings(species.Evolutions),
		PreEvolution: species.PreEvolution,
		Tags:         nonNilStrings(species.Tags),
		Released:     species.Released,
		LeagueRanks:  lookupLeagueRanks(ctx, tool.rankings, species.ID),
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

	for _, league := range leagueRankLookup {
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
