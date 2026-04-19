package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrCyclicEvolution is returned when a cycle is detected while
// walking the evolution tree. pvpoke's gamemaster is acyclic in
// practice, but a defensive guard protects against upstream drift.
var ErrCyclicEvolution = errors.New("evolution tree cycle detected")

// EvolutionPreviewParams is the JSON input for pvp_evolution_preview.
// The caller provides the current (pre-evolution) species, its IVs,
// and an observed CP. The tool inverts CP to level via engine's
// LevelForCP, then projects each reachable descendant's stats at the
// same level (evolution preserves level in Pokémon GO).
type EvolutionPreviewParams struct {
	Species string `json:"species" jsonschema:"the current species id (pre-evolution)"`
	IV      [3]int `json:"iv" jsonschema:"individual values [atk, def, sta]; each 0..15"`
	CP      int    `json:"cp" jsonschema:"observed CP of the current form; inverted to a level"`
	XL      bool   `json:"xl,omitempty" jsonschema:"allow XL candy levels above 40"`
}

// EvolutionStage is one descendant form reachable by evolving the
// input species, projected at the level inverted from CP. Path is
// the chain of species ids from the parent of this stage down to
// this stage inclusive (so a direct evolution has Path of length 1;
// a great-grandparent's terminal descendant has Path of length 3).
type EvolutionStage struct {
	Species     string   `json:"species"`
	Path        []string `json:"path"`
	CP          int      `json:"cp"`
	Atk         float64  `json:"atk"`
	Def         float64  `json:"def"`
	HP          int      `json:"hp"`
	StatProduct float64  `json:"stat_product"`
	LeagueFit   []string `json:"league_fit"`
}

// EvolutionPreviewResult is the JSON output. Level is the inverted
// value; BaseCP is the (re-computed) CP of the input form at that
// level — may be strictly below params.CP when the observed CP does
// not land exactly on the 0.5 grid. Evolutions is sorted by Path
// length (direct evolutions first) and then alphabetically so the
// output is deterministic.
type EvolutionPreviewResult struct {
	Species    string           `json:"species"`
	IV         [3]int           `json:"iv"`
	Level      float64          `json:"level"`
	BaseCP     int              `json:"base_cp"`
	Evolutions []EvolutionStage `json:"evolutions"`
}

// EvolutionPreviewTool wraps the gamemaster manager.
type EvolutionPreviewTool struct {
	manager *gamemaster.Manager
}

// NewEvolutionPreviewTool constructs the tool bound to the gamemaster.
func NewEvolutionPreviewTool(manager *gamemaster.Manager) *EvolutionPreviewTool {
	return &EvolutionPreviewTool{manager: manager}
}

const evolutionPreviewToolDescription = "Given the current species + IVs + observed CP, project each reachable " +
	"descendant form's stats at the same level (evolution preserves level). Returns CP, stats, and the subset of " +
	"standard leagues (little/great/ultra/master) the evolved form fits under, for direct evolutions and every " +
	"transitively-reachable later stage."

// Tool returns the MCP tool registration.
func (tool *EvolutionPreviewTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_evolution_preview",
		Description: evolutionPreviewToolDescription,
	}
}

// Handler returns the MCP-typed handler function.
func (tool *EvolutionPreviewTool) Handler() mcp.ToolHandlerFor[EvolutionPreviewParams, EvolutionPreviewResult] {
	return tool.handle
}

// maxEvolutionDepth caps the BFS walk so a corrupt gamemaster with
// a very deep chain cannot blow out stack or response size. Real
// chains top out at two hops (base → mid → final); five is a
// generous ceiling.
const maxEvolutionDepth = 5

// handle inverts CP to level, walks the evolution tree, projects
// stats at that level for each descendant, and returns the result.
func (tool *EvolutionPreviewTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params EvolutionPreviewParams,
) (*mcp.CallToolResult, EvolutionPreviewResult, error) {
	snapshot, base, ivs, err := tool.resolveEvolutionPreview(ctx, &params)
	if err != nil {
		return nil, EvolutionPreviewResult{}, err
	}

	inverted, err := pogopvp.LevelForCP(base.BaseStats, ivs, params.CP,
		pogopvp.FindSpreadOpts{XLAllowed: params.XL})
	if err != nil {
		return nil, EvolutionPreviewResult{}, fmt.Errorf("level_for_cp: %w", err)
	}

	cpm, err := pogopvp.CPMAt(inverted.Level)
	if err != nil {
		return nil, EvolutionPreviewResult{}, fmt.Errorf("cpm at level %.1f: %w", inverted.Level, err)
	}

	stages, err := collectEvolutionStages(snapshot, base, ivs, cpm)
	if err != nil {
		return nil, EvolutionPreviewResult{}, err
	}

	sort.Slice(stages, func(i, j int) bool {
		if len(stages[i].Path) != len(stages[j].Path) {
			return len(stages[i].Path) < len(stages[j].Path)
		}

		return stages[i].Species < stages[j].Species
	})

	return nil, EvolutionPreviewResult{
		Species:    params.Species,
		IV:         params.IV,
		Level:      inverted.Level,
		BaseCP:     inverted.CP,
		Evolutions: stages,
	}, nil
}

// resolveEvolutionPreview centralises the cheap precondition work
// (cancel check, gamemaster snapshot, species lookup, IV parse) so
// handle stays under funlen. The quadruple return is (snapshot,
// base species, parsed IV, error) — kept unnamed to stay consistent
// with the rest of the resolveX helpers in this package.
func (tool *EvolutionPreviewTool) resolveEvolutionPreview(
	ctx context.Context, params *EvolutionPreviewParams,
) (*pogopvp.Gamemaster, *pogopvp.Species, pogopvp.IV, error) {
	err := ctx.Err()
	if err != nil {
		return nil, nil, pogopvp.IV{}, fmt.Errorf("evolution_preview cancelled: %w", err)
	}

	snapshot := tool.manager.Current()
	if snapshot == nil {
		return nil, nil, pogopvp.IV{}, ErrGamemasterNotLoaded
	}

	base, ok := snapshot.Pokemon[params.Species]
	if !ok {
		return nil, nil, pogopvp.IV{}, fmt.Errorf("%w: %q", ErrUnknownSpecies, params.Species)
	}

	ivs, err := pogopvp.NewIV(params.IV[0], params.IV[1], params.IV[2])
	if err != nil {
		return nil, nil, pogopvp.IV{}, fmt.Errorf("invalid IV: %w", err)
	}

	return snapshot, &base, ivs, nil
}

// evolutionWalkFrame is one BFS frontier node: a reachable species
// id plus the path taken to reach it (parent species first, this
// species last).
type evolutionWalkFrame struct {
	speciesID string
	path      []string
}

// collectEvolutionStages BFS-walks Species.Evolutions, projecting
// stats for each descendant at the shared level (via cpm). Unknown
// species ids in the evolution list are silently skipped — the
// gamemaster refresh and the manager caches can drift by up to a
// day, and a missing link in the chain should not fail the whole
// request.
func collectEvolutionStages(
	snapshot *pogopvp.Gamemaster, base *pogopvp.Species, ivs pogopvp.IV, cpm float64,
) ([]EvolutionStage, error) {
	frontier := make([]evolutionWalkFrame, 0, len(base.Evolutions))
	for _, evoID := range base.Evolutions {
		frontier = append(frontier, evolutionWalkFrame{speciesID: evoID, path: []string{evoID}})
	}

	seen := map[string]bool{base.ID: true}
	stages := make([]EvolutionStage, 0, len(frontier))

	for len(frontier) > 0 {
		frame := frontier[0]
		frontier = frontier[1:]

		if seen[frame.speciesID] {
			return nil, fmt.Errorf("%w: revisited %q via %v",
				ErrCyclicEvolution, frame.speciesID, frame.path)
		}

		seen[frame.speciesID] = true

		evolved, ok := snapshot.Pokemon[frame.speciesID]
		if !ok {
			continue
		}

		stage := projectEvolutionStage(&evolved, ivs, cpm, frame.path)
		stages = append(stages, stage)

		if len(frame.path) >= maxEvolutionDepth {
			continue
		}

		for _, nextID := range evolved.Evolutions {
			nextPath := make([]string, 0, len(frame.path)+1)
			nextPath = append(nextPath, frame.path...)
			nextPath = append(nextPath, nextID)
			frontier = append(frontier, evolutionWalkFrame{speciesID: nextID, path: nextPath})
		}
	}

	return stages, nil
}

// projectEvolutionStage computes stats/CP for `evolved` at the
// pre-resolved CPM (inherited from the base form's level) and
// tags which standard leagues the resulting CP fits under.
func projectEvolutionStage(
	evolved *pogopvp.Species, ivs pogopvp.IV, cpm float64, path []string,
) EvolutionStage {
	stats := pogopvp.ComputeStats(evolved.BaseStats, ivs, cpm)
	combatPower := pogopvp.ComputeCP(evolved.BaseStats, ivs, cpm)

	return EvolutionStage{
		Species:     evolved.ID,
		Path:        append([]string(nil), path...),
		CP:          combatPower,
		Atk:         stats.Atk,
		Def:         stats.Def,
		HP:          stats.HP,
		StatProduct: pogopvp.ComputeStatProduct(stats),
		LeagueFit:   leaguesFitting(combatPower),
	}
}

// leaguesFitting returns the list of standard leagues whose CP cap
// is at or above the given CP, in ascending cap order
// (little → great → ultra → master). Empty slice when CP exceeds
// every cap — pathological since master's cap is the sentinel
// 10000, but kept for defensive correctness.
func leaguesFitting(combatPower int) []string {
	order := []struct {
		name string
		cap  int
	}{
		{"little", littleLeagueCap},
		{"great", greatLeagueCap},
		{"ultra", ultraLeagueCap},
		{"master", masterLeagueCap},
	}

	out := make([]string, 0, len(order))

	for _, entry := range order {
		if combatPower <= entry.cap {
			out = append(out, entry.name)
		}
	}

	return out
}
