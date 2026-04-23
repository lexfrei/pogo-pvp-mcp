package tools

import (
	"context"
	"fmt"
	"sort"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MoveInfoParams is the JSON input contract for pvp_move_info.
type MoveInfoParams struct {
	MoveID string `json:"move_id" jsonschema:"move id in the pvpoke gamemaster (e.g. \"COUNTER\")"`
}

// MoveInfoResult is the JSON output for pvp_move_info. LegacyOnSpecies
// and EliteOnSpecies are disjoint reverse indices: legacy = species
// where this id appears in LegacyMoves (permanently removed), elite =
// species where it appears in EliteMoves (Elite TM / Community Day).
// Both sorted alphabetically so the output is deterministic.
type MoveInfoResult struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	Category        string   `json:"category"`
	Power           int      `json:"power"`
	Energy          int      `json:"energy,omitempty"`
	EnergyGain      int      `json:"energy_gain,omitempty"`
	Cooldown        int      `json:"cooldown,omitempty"`
	Turns           int      `json:"turns,omitempty"`
	LegacyOnSpecies []string `json:"legacy_on_species"`
	EliteOnSpecies  []string `json:"elite_on_species"`
}

// MoveInfoTool is a read-only lookup over gamemaster.
type MoveInfoTool struct {
	gm *gamemaster.Manager
}

// NewMoveInfoTool constructs the tool bound to the given gamemaster
// manager.
func NewMoveInfoTool(gm *gamemaster.Manager) *MoveInfoTool {
	return &MoveInfoTool{gm: gm}
}

const moveInfoToolDescription = "Look up a move in the current gamemaster: type, power, energy, duration, " +
	"plus the reverse index of every species on which this move is flagged legacy."

// Tool returns the MCP registration.
func (tool *MoveInfoTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_move_info",
		Description: moveInfoToolDescription,
	}
}

// Handler returns the MCP-typed handler.
func (tool *MoveInfoTool) Handler() mcp.ToolHandlerFor[MoveInfoParams, MoveInfoResult] {
	return tool.handle
}

// handle orchestrates the lookup. The reverse legacy index is built
// at request time — ~1700 species × slices.Contains against a short
// LegacyMoves slice is ms-level, caching is not worth it pre-v0.1.
func (tool *MoveInfoTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params MoveInfoParams,
) (*mcp.CallToolResult, MoveInfoResult, error) {
	err := ctx.Err()
	if err != nil {
		return nil, MoveInfoResult{}, fmt.Errorf("move_info cancelled: %w", err)
	}

	snapshot := tool.gm.Current()
	if snapshot == nil {
		return nil, MoveInfoResult{}, ErrGamemasterNotLoaded
	}

	move, ok := snapshot.Moves[params.MoveID]
	if !ok {
		return nil, MoveInfoResult{}, fmt.Errorf("%w: %q", ErrUnknownMove, params.MoveID)
	}

	return nil, MoveInfoResult{
		ID:              move.ID,
		Name:            move.Name,
		Type:            move.Type,
		Category:        categoryLabel(move.Category),
		Power:           move.Power,
		Energy:          move.Energy,
		EnergyGain:      move.EnergyGain,
		Cooldown:        move.Cooldown,
		Turns:           move.Turns,
		LegacyOnSpecies: reverseIndexByCategory(snapshot, move.ID, pogopvp.IsLegacyMove),
		EliteOnSpecies:  reverseIndexByCategory(snapshot, move.ID, pogopvp.IsEliteMove),
	}, nil
}

// categoryLabel maps the engine MoveCategory onto the MCP-facing
// string. Unknown values collapse to "unknown" rather than surfacing
// integer codes to clients.
func categoryLabel(category pogopvp.MoveCategory) string {
	switch category {
	case pogopvp.MoveCategoryFast:
		return "fast"
	case pogopvp.MoveCategoryCharged:
		return "charged"
	default:
		return "unknown"
	}
}

// reverseIndexByCategory walks every species in the gamemaster and
// collects the ids where predicate(species, moveID)=true. Sorted
// alphabetically so the output is deterministic. Pre-allocates a
// zero-length non-nil slice so the JSON wire shape is always `[]`
// rather than `null` — downstream LLM clients parse list-typed
// fields more reliably against `[]` than `null`.
//
// Used by pvp_move_info to build both legacy_on_species and
// elite_on_species reverse indices with the same loop shape.
//
// The `for id, species := range` idiom binds a fresh species copy
// each iteration; we pass &species to the predicate because Go does
// not allow taking the address of a map-value expression directly
// (`&snapshot.Pokemon[id]` is a compile error), and the predicates
// read the species without mutating it so the copy is fine.
func reverseIndexByCategory(
	snapshot *pogopvp.Gamemaster, moveID string,
	predicate func(*pogopvp.Species, string) bool,
) []string {
	out := make([]string, 0)

	// rangeValCopy is unavoidable here: Go forbids &snapshot.Pokemon[id]
	// directly (map-value expressions are not addressable) and the
	// predicate needs a *Species. The copy is read-only — the
	// predicate only performs a slices.Contains over the relevant
	// restricted-move slice.
	//nolint:gocritic // see comment above — idiomatic map-iter workaround
	for id, species := range snapshot.Pokemon {
		if predicate(&species, moveID) {
			out = append(out, id)
		}
	}

	sort.Strings(out)

	return out
}
