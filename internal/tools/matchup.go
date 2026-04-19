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

// ErrUnknownMove is returned when a move id referenced by MatchupParams
// is not present in the currently loaded gamemaster.
var ErrUnknownMove = errors.New("unknown move")

// ErrMoveCategoryMismatch is returned when a caller passes a charged
// move id in the fast slot or vice versa. Silent acceptance would
// surface as a non-fire charged move or a spurious "EnergyGain=0"
// engine error downstream.
var ErrMoveCategoryMismatch = errors.New("move category mismatch")

// Combatant is the MCP-visible input shape for one fighter in a
// matchup / team. Shields on matchup is specified once per side at
// the MatchupParams level so callers can sweep (0, 1, 2)×(0, 1, 2)
// outside the tool. FastMove and ChargedMoves are optional: if left
// empty the tool resolves the recommended moveset from the relevant
// (cup, cap) rankings entry. An explicit move overrides the default.
type Combatant struct {
	Species      string   `json:"species" jsonschema:"species id"`
	IV           [3]int   `json:"iv" jsonschema:"IV triple [atk, def, sta]"`
	Level        float64  `json:"level" jsonschema:"level on 0.5 grid, [1.0, 51.0]"`
	FastMove     string   `json:"fast_move,omitempty" jsonschema:"fast move id; omit to use recommended"`
	ChargedMoves []string `json:"charged_moves,omitempty" jsonschema:"charged move ids; omit to use recommended"`
}

// ResolvedCombatant echoes back the species + moveset actually used
// by the engine after any recommended-moveset defaulting. Callers
// consume it to learn which fast/charged pair the tool picked when
// they left those fields empty on input.
type ResolvedCombatant struct {
	Species      string   `json:"species"`
	FastMove     string   `json:"fast_move"`
	ChargedMoves []string `json:"charged_moves"`
}

// resolvedFromSpec projects a finalised Combatant (after moveset
// defaulting) to its echo shape.
func resolvedFromSpec(spec *Combatant) ResolvedCombatant {
	charged := make([]string, len(spec.ChargedMoves))
	copy(charged, spec.ChargedMoves)

	return ResolvedCombatant{
		Species:      spec.Species,
		FastMove:     spec.FastMove,
		ChargedMoves: charged,
	}
}

// MatchupParams is the JSON input contract for pvp_matchup. Cup is
// used only to pick the recommended moveset when a Combatant omits
// its moves — cup rules do not otherwise modify simulation mechanics.
type MatchupParams struct {
	Attacker Combatant `json:"attacker"`
	Defender Combatant `json:"defender"`
	League   string    `json:"league,omitempty" jsonschema:"little|great|ultra|master; required when movesets are omitted"`
	Cup      string    `json:"cup,omitempty" jsonschema:"cup id from pvpoke; used for recommended moveset lookup"`
	Shields  [2]int    `json:"shields,omitempty" jsonschema:"shield counts [attacker, defender], each 0..2"`
	MaxTurns int       `json:"max_turns,omitempty" jsonschema:"simulation turn cap; 0 uses engine default"`
}

// MatchupResult is the JSON output contract for pvp_matchup. Attacker
// and Defender echo the resolved moveset so callers that omitted one
// can see what was used.
type MatchupResult struct {
	Winner       string            `json:"winner"`
	Turns        int               `json:"turns"`
	HPRemaining  [2]int            `json:"hp_remaining"`
	EnergyAtEnd  [2]int            `json:"energy_at_end"`
	ShieldsUsed  [2]int            `json:"shields_used"`
	ChargedFired [2]int            `json:"charged_fired"`
	Attacker     ResolvedCombatant `json:"attacker"`
	Defender     ResolvedCombatant `json:"defender"`
}

// MatchupTool wraps the gamemaster plus an optional rankings
// manager. When rankings is nil the tool behaves as pre-Phase-C:
// every Combatant must carry an explicit moveset, otherwise the
// handler errors with ErrNoRecommendedMoveset.
type MatchupTool struct {
	manager  *gamemaster.Manager
	rankings *rankings.Manager
}

// NewMatchupTool returns a MatchupTool bound to the given managers.
// ranks may be nil in tests that supply explicit movesets.
func NewMatchupTool(manager *gamemaster.Manager, ranks *rankings.Manager) *MatchupTool {
	return &MatchupTool{manager: manager, rankings: ranks}
}

// matchupToolDescription is factored out so the struct-literal stays
// under the line-length limit.
const matchupToolDescription = "Simulate a 1v1 PvP matchup between two Pokémon with their IVs, " +
	"levels, moves, and shield counts. Returns winner, turns, remaining HP, energy, " +
	"shields used, and charged-move firing counts."

// Tool returns the MCP tool registration.
func (tool *MatchupTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_matchup",
		Description: matchupToolDescription,
	}
}

// Handler returns the MCP-typed handler function.
func (tool *MatchupTool) Handler() mcp.ToolHandlerFor[MatchupParams, MatchupResult] {
	return tool.handle
}

// handle orchestrates the pvp_matchup simulation. Honours context
// cancellation on entry and after the engine Simulate returns so a
// client disconnect releases the worker promptly.
func (tool *MatchupTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params MatchupParams,
) (*mcp.CallToolResult, MatchupResult, error) {
	err := ctx.Err()
	if err != nil {
		return nil, MatchupResult{}, fmt.Errorf("matchup cancelled: %w", err)
	}

	snapshot := tool.manager.Current()
	if snapshot == nil {
		return nil, MatchupResult{}, ErrGamemasterNotLoaded
	}

	err = tool.applyDefaults(ctx, &params)
	if err != nil {
		return nil, MatchupResult{}, err
	}

	attacker, err := buildEngineCombatant(snapshot, &params.Attacker, params.Shields[0])
	if err != nil {
		return nil, MatchupResult{}, fmt.Errorf("attacker: %w", err)
	}

	defender, err := buildEngineCombatant(snapshot, &params.Defender, params.Shields[1])
	if err != nil {
		return nil, MatchupResult{}, fmt.Errorf("defender: %w", err)
	}

	result, err := pogopvp.Simulate(&attacker, &defender, pogopvp.BattleOptions{MaxTurns: params.MaxTurns})
	if err != nil {
		return nil, MatchupResult{}, fmt.Errorf("simulate: %w", err)
	}

	return nil, MatchupResult{
		Winner:       winnerLabel(result.Winner),
		Turns:        result.Turns,
		HPRemaining:  result.HPRemaining,
		EnergyAtEnd:  result.EnergyAtEnd,
		ShieldsUsed:  result.ShieldsUsed,
		ChargedFired: result.ChargedFired,
		Attacker:     resolvedFromSpec(&params.Attacker),
		Defender:     resolvedFromSpec(&params.Defender),
	}, nil
}

// applyDefaults fills in missing movesets on attacker / defender by
// consulting the rankings manager when Cup/League let us resolve a
// CP cap. Resolution is triggered only by an empty FastMove — an
// empty ChargedMoves with a set FastMove is a legitimate fast-only
// build and is left alone. If neither side needs resolution this is
// a no-op and League may be empty.
func (tool *MatchupTool) applyDefaults(ctx context.Context, params *MatchupParams) error {
	needsResolve := params.Attacker.FastMove == "" || params.Defender.FastMove == ""
	if !needsResolve {
		return nil
	}

	if params.League == "" {
		return fmt.Errorf("%w: league is required when combatant moves are omitted",
			ErrUnknownLeague)
	}

	cpCap, err := resolveCPCap(params.League, 0)
	if err != nil {
		return err
	}

	err = applyMovesetDefaults(ctx, tool.rankings, &params.Attacker, cpCap, params.Cup)
	if err != nil {
		return fmt.Errorf("attacker moveset: %w", err)
	}

	err = applyMovesetDefaults(ctx, tool.rankings, &params.Defender, cpCap, params.Cup)
	if err != nil {
		return fmt.Errorf("defender moveset: %w", err)
	}

	return nil
}

// buildEngineCombatant maps a tool-level Combatant (string-addressed
// moves, species id) to an engine-level pogopvp.Combatant with looked-up
// Species, Move structs, and a validated IV.
func buildEngineCombatant(
	snapshot *pogopvp.Gamemaster, spec *Combatant, shields int,
) (pogopvp.Combatant, error) {
	species, ok := snapshot.Pokemon[spec.Species]
	if !ok {
		return pogopvp.Combatant{}, fmt.Errorf("%w: %q", ErrUnknownSpecies, spec.Species)
	}

	ivs, err := pogopvp.NewIV(spec.IV[0], spec.IV[1], spec.IV[2])
	if err != nil {
		return pogopvp.Combatant{}, fmt.Errorf("invalid IV: %w", err)
	}

	fast, ok := snapshot.Moves[spec.FastMove]
	if !ok {
		return pogopvp.Combatant{}, fmt.Errorf("%w: fast %q", ErrUnknownMove, spec.FastMove)
	}

	if fast.Category != pogopvp.MoveCategoryFast {
		return pogopvp.Combatant{}, fmt.Errorf(
			"%w: %q is a charged move, but was passed as fast_move",
			ErrMoveCategoryMismatch, spec.FastMove)
	}

	charged := make([]pogopvp.Move, 0, len(spec.ChargedMoves))

	for _, moveID := range spec.ChargedMoves {
		move, moveOK := snapshot.Moves[moveID]
		if !moveOK {
			return pogopvp.Combatant{}, fmt.Errorf("%w: charged %q", ErrUnknownMove, moveID)
		}

		if move.Category != pogopvp.MoveCategoryCharged {
			return pogopvp.Combatant{}, fmt.Errorf(
				"%w: %q is a fast move, but was passed in charged_moves",
				ErrMoveCategoryMismatch, moveID)
		}

		charged = append(charged, move)
	}

	return pogopvp.Combatant{
		Species:      species,
		IV:           ivs,
		Level:        spec.Level,
		FastMove:     fast,
		ChargedMoves: charged,
		Shields:      shields,
	}, nil
}

// winnerLabel maps the engine's integer winner code to the JSON-facing
// label: "attacker" (0), "defender" (1), "tie" (simultaneous faint),
// "timeout" (MaxTurns elapsed with both alive). Any other code is a
// signal that the engine added a new sentinel without updating the
// MCP-facing mapping — the caller gets a distinct "unknown:<code>"
// string rather than being silently folded into "tie".
func winnerLabel(code int) string {
	switch code {
	case 0:
		return "attacker"
	case 1:
		return "defender"
	case pogopvp.BattleTie:
		return "tie"
	case pogopvp.BattleTimeout:
		return "timeout"
	default:
		return fmt.Sprintf("unknown:%d", code)
	}
}
