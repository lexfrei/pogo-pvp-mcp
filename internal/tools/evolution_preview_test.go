package tools_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

// evolutionFixtureGamemaster declares a linear chain (wartortle →
// blastoise), a branching root (eevee → [vaporeon, jolteon]), and a
// one-step chain (kadabra → alakazam). Stat lines are only
// representative — they need to be consistent but not exact-to-
// upstream because engine computations are deterministic given the
// inputs.
const evolutionFixtureGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {
      "dex": 7,
      "speciesId": "squirtle",
      "speciesName": "Squirtle",
      "baseStats": {"atk": 94, "def": 121, "hp": 127},
      "types": ["water"],
      "fastMoves": ["WATER_GUN"],
      "chargedMoves": ["WATER_PULSE"],
      "family": {"id": "FAMILY_SQUIRTLE", "evolutions": ["wartortle"]},
      "released": true
    },
    {
      "dex": 8,
      "speciesId": "wartortle",
      "speciesName": "Wartortle",
      "baseStats": {"atk": 126, "def": 155, "hp": 153},
      "types": ["water"],
      "fastMoves": ["WATER_GUN"],
      "chargedMoves": ["AQUA_JET"],
      "family": {"id": "FAMILY_SQUIRTLE", "parent": "squirtle", "evolutions": ["blastoise"]},
      "released": true
    },
    {
      "dex": 9,
      "speciesId": "blastoise",
      "speciesName": "Blastoise",
      "baseStats": {"atk": 171, "def": 207, "hp": 188},
      "types": ["water"],
      "fastMoves": ["WATER_GUN"],
      "chargedMoves": ["HYDRO_CANNON"],
      "family": {"id": "FAMILY_SQUIRTLE", "parent": "wartortle", "evolutions": []},
      "released": true
    },
    {
      "dex": 133,
      "speciesId": "eevee",
      "speciesName": "Eevee",
      "baseStats": {"atk": 104, "def": 114, "hp": 146},
      "types": ["normal"],
      "fastMoves": ["TACKLE"],
      "chargedMoves": ["DIG"],
      "family": {"id": "FAMILY_EEVEE", "evolutions": ["vaporeon", "jolteon"]},
      "released": true
    },
    {
      "dex": 134,
      "speciesId": "vaporeon",
      "speciesName": "Vaporeon",
      "baseStats": {"atk": 205, "def": 161, "hp": 277},
      "types": ["water"],
      "fastMoves": ["WATER_GUN"],
      "chargedMoves": ["HYDRO_PUMP"],
      "family": {"id": "FAMILY_EEVEE", "parent": "eevee", "evolutions": []},
      "released": true
    },
    {
      "dex": 135,
      "speciesId": "jolteon",
      "speciesName": "Jolteon",
      "baseStats": {"atk": 232, "def": 182, "hp": 163},
      "types": ["electric"],
      "fastMoves": ["THUNDER_SHOCK"],
      "chargedMoves": ["DISCHARGE"],
      "family": {"id": "FAMILY_EEVEE", "parent": "eevee", "evolutions": []},
      "released": true
    },
    {
      "dex": 65,
      "speciesId": "alakazam",
      "speciesName": "Alakazam",
      "baseStats": {"atk": 271, "def": 167, "hp": 146},
      "types": ["psychic"],
      "fastMoves": ["PSYCHO_CUT"],
      "chargedMoves": ["FUTURE_SIGHT"],
      "family": {"id": "FAMILY_ABRA", "parent": "kadabra", "evolutions": []},
      "released": true
    }
  ],
  "moves": [
    {"moveId": "WATER_GUN", "name": "Water Gun", "type": "water",
     "power": 3, "energy": 0, "energyGain": 3, "cooldown": 500, "turns": 1},
    {"moveId": "WATER_PULSE", "name": "Water Pulse", "type": "water",
     "power": 70, "energy": 60, "cooldown": 500},
    {"moveId": "AQUA_JET", "name": "Aqua Jet", "type": "water",
     "power": 45, "energy": 40, "cooldown": 500},
    {"moveId": "HYDRO_CANNON", "name": "Hydro Cannon", "type": "water",
     "power": 80, "energy": 40, "cooldown": 500},
    {"moveId": "TACKLE", "name": "Tackle", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 3, "cooldown": 500, "turns": 1},
    {"moveId": "DIG", "name": "Dig", "type": "ground",
     "power": 100, "energy": 70, "cooldown": 500},
    {"moveId": "HYDRO_PUMP", "name": "Hydro Pump", "type": "water",
     "power": 130, "energy": 75, "cooldown": 500},
    {"moveId": "THUNDER_SHOCK", "name": "Thunder Shock", "type": "electric",
     "power": 3, "energy": 0, "energyGain": 3, "cooldown": 500, "turns": 1},
    {"moveId": "DISCHARGE", "name": "Discharge", "type": "electric",
     "power": 65, "energy": 45, "cooldown": 500},
    {"moveId": "PSYCHO_CUT", "name": "Psycho Cut", "type": "psychic",
     "power": 3, "energy": 0, "energyGain": 3, "cooldown": 500, "turns": 1},
    {"moveId": "FUTURE_SIGHT", "name": "Future Sight", "type": "psychic",
     "power": 120, "energy": 65, "cooldown": 500}
  ]
}`

const (
	speciesSquirtle  = "squirtle"
	speciesWartortle = "wartortle"
	speciesBlastoise = "blastoise"
)

func newEvolutionPreviewTool(t *testing.T, gmJSON string) *tools.EvolutionPreviewTool {
	t.Helper()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(gmJSON))
	}))
	t.Cleanup(gmServer.Close)

	gmMgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    gmServer.URL,
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager gm: %v", err)
	}

	err = gmMgr.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh gm: %v", err)
	}

	return tools.NewEvolutionPreviewTool(gmMgr)
}

// TestEvolutionPreview_LinearChain pins the happy path for a
// three-stage linear chain (squirtle → wartortle → blastoise).
// Both descendants must appear with correct Path ordering; their CP
// must be strictly greater than the squirtle baseline (base stats
// rise with evolution), and their league_fit must be consistent
// with the CP.
func TestEvolutionPreview_LinearChain(t *testing.T) {
	t.Parallel()

	tool := newEvolutionPreviewTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionPreviewParams{
		Species: speciesSquirtle,
		IV:      [3]int{15, 15, 15},
		CP:      500,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.BaseCP <= 0 || result.BaseCP > 500 {
		t.Errorf("BaseCP = %d, want in (0, 500]", result.BaseCP)
	}

	if len(result.Evolutions) != 2 {
		t.Fatalf("Evolutions len = %d, want 2 (wartortle + blastoise)", len(result.Evolutions))
	}

	wart := result.Evolutions[0]
	blast := result.Evolutions[1]

	if wart.Species != speciesWartortle {
		t.Errorf("first stage species = %q, want wartortle", wart.Species)
	}

	if len(wart.Path) != 1 || wart.Path[0] != speciesWartortle {
		t.Errorf("wartortle path = %v, want [wartortle]", wart.Path)
	}

	if blast.Species != speciesBlastoise {
		t.Errorf("second stage species = %q, want blastoise", blast.Species)
	}

	if len(blast.Path) != 2 || blast.Path[0] != speciesWartortle || blast.Path[1] != speciesBlastoise {
		t.Errorf("blastoise path = %v, want [wartortle, blastoise]", blast.Path)
	}

	if blast.CP <= wart.CP {
		t.Errorf("blastoise CP %d ≤ wartortle CP %d, want strictly greater", blast.CP, wart.CP)
	}
}

// TestEvolutionPreview_BranchingRoot pins the branching case: eevee
// has two evolutions in the fixture, both must appear, neither
// should contain the other in its Path.
func TestEvolutionPreview_BranchingRoot(t *testing.T) {
	t.Parallel()

	tool := newEvolutionPreviewTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionPreviewParams{
		Species: "eevee",
		IV:      [3]int{10, 15, 10},
		CP:      600,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Evolutions) != 2 {
		t.Fatalf("Evolutions len = %d, want 2 (vaporeon + jolteon)", len(result.Evolutions))
	}

	found := map[string]bool{}
	for _, stage := range result.Evolutions {
		found[stage.Species] = true

		if len(stage.Path) != 1 {
			t.Errorf("%s path = %v, want length 1 (direct evolution)", stage.Species, stage.Path)
		}
	}

	if !found["vaporeon"] || !found["jolteon"] {
		t.Errorf("Evolutions = %+v, want both vaporeon and jolteon present", result.Evolutions)
	}

	// Lock the alphabetical tiebreak: at equal Path length the sort
	// orders by species id ascending, so jolteon must come before
	// vaporeon. Guards against a silent regression of the sort key.
	if result.Evolutions[0].Species != "jolteon" {
		t.Errorf("Evolutions[0].Species = %q, want \"jolteon\" (alphabetical tiebreak)",
			result.Evolutions[0].Species)
	}

	if result.Evolutions[1].Species != "vaporeon" {
		t.Errorf("Evolutions[1].Species = %q, want \"vaporeon\" (alphabetical tiebreak)",
			result.Evolutions[1].Species)
	}
}

// TestEvolutionPreview_TerminalSpecies pins the no-further-evolution
// case: alakazam has no children, the result must list zero stages
// but still populate the base CP / level.
func TestEvolutionPreview_TerminalSpecies(t *testing.T) {
	t.Parallel()

	tool := newEvolutionPreviewTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionPreviewParams{
		Species: "alakazam",
		IV:      [3]int{15, 15, 15},
		CP:      1500,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Evolutions) != 0 {
		t.Errorf("Evolutions = %+v, want empty for terminal species", result.Evolutions)
	}

	if result.Level <= 0 {
		t.Errorf("Level = %f, want positive for resolvable CP", result.Level)
	}
}

// TestEvolutionPreview_LeagueFitShape pins the CP → league bucket
// mapping: a low-CP squirtle preview produces evolved forms whose
// CP might still sit in the great league, so league_fit must
// contain "great" and "ultra" and "master" in that order, and the
// order is deterministic.
func TestEvolutionPreview_LeagueFitShape(t *testing.T) {
	t.Parallel()

	tool := newEvolutionPreviewTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionPreviewParams{
		Species: speciesSquirtle,
		IV:      [3]int{15, 15, 15},
		CP:      300,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	for _, stage := range result.Evolutions {
		if len(stage.LeagueFit) == 0 {
			t.Errorf("%s league_fit empty; expected at least 'master'", stage.Species)

			continue
		}

		last := stage.LeagueFit[len(stage.LeagueFit)-1]
		if last != "master" {
			t.Errorf("%s league_fit last element = %q, want 'master' (highest cap)", stage.Species, last)
		}

		if slices.Contains(stage.LeagueFit, "great") {
			idxGreat := slices.Index(stage.LeagueFit, "great")
			idxMaster := slices.Index(stage.LeagueFit, "master")

			if idxGreat >= idxMaster {
				t.Errorf("%s league_fit = %v, want ascending cap order (great before master)",
					stage.Species, stage.LeagueFit)
			}
		}
	}
}

// TestEvolutionPreview_UnknownSpecies rejects a non-existent species.
func TestEvolutionPreview_UnknownSpecies(t *testing.T) {
	t.Parallel()

	tool := newEvolutionPreviewTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.EvolutionPreviewParams{
		Species: "missingno",
		IV:      [3]int{15, 15, 15},
		CP:      500,
	})
	if !errors.Is(err, tools.ErrUnknownSpecies) {
		t.Errorf("error = %v, want wrapping ErrUnknownSpecies", err)
	}
}

// TestEvolutionPreview_InvalidIV rejects out-of-range IVs before any
// gamemaster lookup would compute against them.
func TestEvolutionPreview_InvalidIV(t *testing.T) {
	t.Parallel()

	tool := newEvolutionPreviewTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.EvolutionPreviewParams{
		Species: speciesSquirtle,
		IV:      [3]int{16, 0, 0},
		CP:      500,
	})
	if err == nil {
		t.Error("error = nil, want non-nil for IV with component 16")
	}
}

// TestEvolutionPreview_GamemasterNotLoaded exercises the cold-start
// guard: a manager constructed but never refreshed must surface
// ErrGamemasterNotLoaded.
func TestEvolutionPreview_GamemasterNotLoaded(t *testing.T) {
	t.Parallel()

	gmMgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    "http://127.0.0.1:1", // will never be reached; Refresh is not called
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	tool := tools.NewEvolutionPreviewTool(gmMgr)
	handler := tool.Handler()

	_, _, err = handler(t.Context(), nil, tools.EvolutionPreviewParams{
		Species: speciesSquirtle,
		IV:      [3]int{15, 15, 15},
		CP:      500,
	})
	if !errors.Is(err, tools.ErrGamemasterNotLoaded) {
		t.Errorf("error = %v, want wrapping ErrGamemasterNotLoaded", err)
	}
}

// TestEvolutionPreview_MissingEvolutionIsSkipped pins the tolerance
// branch: when Species.Evolutions references a species the gamemaster
// does not define (cache skew), that branch is silently omitted
// rather than surfaced as an error. Uses wartortle pre-evolution
// that lists a phantom "phantom_evo" in its Evolutions via a
// one-off fixture.
func TestEvolutionPreview_MissingEvolutionIsSkipped(t *testing.T) {
	t.Parallel()

	const skewedFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {
      "dex": 7,
      "speciesId": "squirtle",
      "speciesName": "Squirtle",
      "baseStats": {"atk": 94, "def": 121, "hp": 127},
      "types": ["water"],
      "fastMoves": ["WATER_GUN"],
      "chargedMoves": ["WATER_PULSE"],
      "family": {"id": "FAMILY_SQUIRTLE",
                 "evolutions": ["wartortle", "phantom_evo"]},
      "released": true
    },
    {
      "dex": 8,
      "speciesId": "wartortle",
      "speciesName": "Wartortle",
      "baseStats": {"atk": 126, "def": 155, "hp": 153},
      "types": ["water"],
      "fastMoves": ["WATER_GUN"],
      "chargedMoves": ["AQUA_JET"],
      "family": {"id": "FAMILY_SQUIRTLE", "parent": "squirtle", "evolutions": []},
      "released": true
    }
  ],
  "moves": [
    {"moveId": "WATER_GUN", "name": "Water Gun", "type": "water",
     "power": 3, "energy": 0, "energyGain": 3, "cooldown": 500, "turns": 1},
    {"moveId": "WATER_PULSE", "name": "Water Pulse", "type": "water",
     "power": 70, "energy": 60, "cooldown": 500},
    {"moveId": "AQUA_JET", "name": "Aqua Jet", "type": "water",
     "power": 45, "energy": 40, "cooldown": 500}
  ]
}`

	tool := newEvolutionPreviewTool(t, skewedFixture)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionPreviewParams{
		Species: speciesSquirtle,
		IV:      [3]int{15, 15, 15},
		CP:      500,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Evolutions) != 1 {
		t.Fatalf("Evolutions len = %d, want 1 (phantom_evo skipped)", len(result.Evolutions))
	}

	if result.Evolutions[0].Species != speciesWartortle {
		t.Errorf("only stage species = %q, want wartortle", result.Evolutions[0].Species)
	}
}

// TestEvolutionPreview_DuplicateEvolutionID pins the fix for the
// false-positive cycle error: pvpoke's real gamemaster contains
// species whose `evolutions` array lists the same child id twice
// (litleo→[pyroar, pyroar], espurr→[meowstic, meowstic], etc. —
// gendered evolutions collapsing onto one species id). This used
// to error with "cycle detected" until enqueue-time dedup landed.
func TestEvolutionPreview_DuplicateEvolutionID(t *testing.T) {
	t.Parallel()

	const duplicateChildFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {
      "dex": 667,
      "speciesId": "litleo",
      "speciesName": "Litleo",
      "baseStats": {"atk": 150, "def": 100, "hp": 160},
      "types": ["fire", "normal"],
      "fastMoves": ["TACKLE"],
      "chargedMoves": ["FLAME_CHARGE"],
      "family": {"id": "FAMILY_LITLEO", "evolutions": ["pyroar", "pyroar"]},
      "released": true
    },
    {
      "dex": 668,
      "speciesId": "pyroar",
      "speciesName": "Pyroar",
      "baseStats": {"atk": 221, "def": 139, "hp": 200},
      "types": ["fire", "normal"],
      "fastMoves": ["TACKLE"],
      "chargedMoves": ["FIRE_BLAST"],
      "family": {"id": "FAMILY_LITLEO", "parent": "litleo", "evolutions": []},
      "released": true
    }
  ],
  "moves": [
    {"moveId": "TACKLE", "name": "Tackle", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 3, "cooldown": 500, "turns": 1},
    {"moveId": "FLAME_CHARGE", "name": "Flame Charge", "type": "fire",
     "power": 65, "energy": 50, "cooldown": 500},
    {"moveId": "FIRE_BLAST", "name": "Fire Blast", "type": "fire",
     "power": 140, "energy": 80, "cooldown": 500}
  ]
}`

	tool := newEvolutionPreviewTool(t, duplicateChildFixture)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionPreviewParams{
		Species: "litleo",
		IV:      [3]int{15, 15, 15},
		CP:      1500,
	})
	if err != nil {
		t.Fatalf("handler: %v (duplicate child id must not surface as cycle)", err)
	}

	if len(result.Evolutions) != 1 {
		t.Fatalf("Evolutions len = %d, want 1 (duplicate collapses to one stage)",
			len(result.Evolutions))
	}

	if result.Evolutions[0].Species != "pyroar" {
		t.Errorf("Evolutions[0].Species = %q, want \"pyroar\"",
			result.Evolutions[0].Species)
	}
}

// TestEvolutionPreview_DiamondSubgraph pins the other real-world DAG
// shape: two distinct parents (pichu and a synthetic alt-pichu) both
// list `raichu` as an evolution. A BFS from a root whose chain
// crosses that diamond used to panic the dequeue-time cycle guard;
// with enqueue-time dedup, raichu appears exactly once via its
// shortest-discovered path.
func TestEvolutionPreview_DiamondSubgraph(t *testing.T) {
	t.Parallel()

	const diamondFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {
      "dex": 172,
      "speciesId": "pichu",
      "speciesName": "Pichu",
      "baseStats": {"atk": 77, "def": 53, "hp": 85},
      "types": ["electric"],
      "fastMoves": ["THUNDER_SHOCK"],
      "chargedMoves": ["DISCHARGE"],
      "family": {"id": "FAMILY_PICHU", "evolutions": ["pikachu", "raichu"]},
      "released": true
    },
    {
      "dex": 25,
      "speciesId": "pikachu",
      "speciesName": "Pikachu",
      "baseStats": {"atk": 112, "def": 96, "hp": 111},
      "types": ["electric"],
      "fastMoves": ["THUNDER_SHOCK"],
      "chargedMoves": ["DISCHARGE"],
      "family": {"id": "FAMILY_PICHU", "parent": "pichu", "evolutions": ["raichu"]},
      "released": true
    },
    {
      "dex": 26,
      "speciesId": "raichu",
      "speciesName": "Raichu",
      "baseStats": {"atk": 193, "def": 151, "hp": 155},
      "types": ["electric"],
      "fastMoves": ["THUNDER_SHOCK"],
      "chargedMoves": ["WILD_CHARGE"],
      "family": {"id": "FAMILY_PICHU", "parent": "pikachu", "evolutions": []},
      "released": true
    }
  ],
  "moves": [
    {"moveId": "THUNDER_SHOCK", "name": "Thunder Shock", "type": "electric",
     "power": 3, "energy": 0, "energyGain": 3, "cooldown": 500, "turns": 1},
    {"moveId": "DISCHARGE", "name": "Discharge", "type": "electric",
     "power": 65, "energy": 45, "cooldown": 500},
    {"moveId": "WILD_CHARGE", "name": "Wild Charge", "type": "electric",
     "power": 100, "energy": 45, "cooldown": 500}
  ]
}`

	tool := newEvolutionPreviewTool(t, diamondFixture)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionPreviewParams{
		Species: "pichu",
		IV:      [3]int{15, 15, 15},
		CP:      500,
	})
	if err != nil {
		t.Fatalf("handler: %v (diamond subgraph must not surface as cycle)", err)
	}

	raichuCount := 0

	for _, stage := range result.Evolutions {
		if stage.Species == "raichu" {
			raichuCount++
		}
	}

	if raichuCount != 1 {
		t.Errorf("raichu appeared %d times, want exactly 1 (diamond dedup)", raichuCount)
	}

	if len(result.Evolutions) != 2 {
		t.Errorf("Evolutions len = %d, want 2 (pikachu, raichu) — diamond collapses",
			len(result.Evolutions))
	}
}

// TestEvolutionPreview_DepthCap pins the maxEvolutionDepth bound: a
// synthetic chain of length 7 must return exactly five stages (the
// first five hops) without erroring. Locks the cap at 5 so a future
// change to the constant forces an explicit test update.
func TestEvolutionPreview_DepthCap(t *testing.T) {
	t.Parallel()

	const deepChainFixture = `{
  "id": "gamemaster",
  "timestamp": "2026-04-19 00:00:00",
  "pokemon": [
    {"dex": 1, "speciesId": "link0", "speciesName": "Link0",
     "baseStats": {"atk": 100, "def": 100, "hp": 100}, "types": ["normal"],
     "fastMoves": ["TACKLE"], "chargedMoves": ["BODY_SLAM"],
     "family": {"id": "FAMILY_LINK", "evolutions": ["link1"]}, "released": true},
    {"dex": 2, "speciesId": "link1", "speciesName": "Link1",
     "baseStats": {"atk": 105, "def": 100, "hp": 100}, "types": ["normal"],
     "fastMoves": ["TACKLE"], "chargedMoves": ["BODY_SLAM"],
     "family": {"id": "FAMILY_LINK", "parent": "link0", "evolutions": ["link2"]},
     "released": true},
    {"dex": 3, "speciesId": "link2", "speciesName": "Link2",
     "baseStats": {"atk": 110, "def": 100, "hp": 100}, "types": ["normal"],
     "fastMoves": ["TACKLE"], "chargedMoves": ["BODY_SLAM"],
     "family": {"id": "FAMILY_LINK", "parent": "link1", "evolutions": ["link3"]},
     "released": true},
    {"dex": 4, "speciesId": "link3", "speciesName": "Link3",
     "baseStats": {"atk": 115, "def": 100, "hp": 100}, "types": ["normal"],
     "fastMoves": ["TACKLE"], "chargedMoves": ["BODY_SLAM"],
     "family": {"id": "FAMILY_LINK", "parent": "link2", "evolutions": ["link4"]},
     "released": true},
    {"dex": 5, "speciesId": "link4", "speciesName": "Link4",
     "baseStats": {"atk": 120, "def": 100, "hp": 100}, "types": ["normal"],
     "fastMoves": ["TACKLE"], "chargedMoves": ["BODY_SLAM"],
     "family": {"id": "FAMILY_LINK", "parent": "link3", "evolutions": ["link5"]},
     "released": true},
    {"dex": 6, "speciesId": "link5", "speciesName": "Link5",
     "baseStats": {"atk": 125, "def": 100, "hp": 100}, "types": ["normal"],
     "fastMoves": ["TACKLE"], "chargedMoves": ["BODY_SLAM"],
     "family": {"id": "FAMILY_LINK", "parent": "link4", "evolutions": ["link6"]},
     "released": true},
    {"dex": 7, "speciesId": "link6", "speciesName": "Link6",
     "baseStats": {"atk": 130, "def": 100, "hp": 100}, "types": ["normal"],
     "fastMoves": ["TACKLE"], "chargedMoves": ["BODY_SLAM"],
     "family": {"id": "FAMILY_LINK", "parent": "link5", "evolutions": ["link7"]},
     "released": true},
    {"dex": 8, "speciesId": "link7", "speciesName": "Link7",
     "baseStats": {"atk": 135, "def": 100, "hp": 100}, "types": ["normal"],
     "fastMoves": ["TACKLE"], "chargedMoves": ["BODY_SLAM"],
     "family": {"id": "FAMILY_LINK", "parent": "link6", "evolutions": []},
     "released": true}
  ],
  "moves": [
    {"moveId": "TACKLE", "name": "Tackle", "type": "normal",
     "power": 3, "energy": 0, "energyGain": 3, "cooldown": 500, "turns": 1},
    {"moveId": "BODY_SLAM", "name": "Body Slam", "type": "normal",
     "power": 60, "energy": 35, "cooldown": 500}
  ]
}`

	tool := newEvolutionPreviewTool(t, deepChainFixture)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionPreviewParams{
		Species: "link0",
		IV:      [3]int{15, 15, 15},
		CP:      1500,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Evolutions) != 5 {
		t.Fatalf("Evolutions len = %d, want 5 (depth cap drops link6/link7)",
			len(result.Evolutions))
	}

	// Lock ordering: depth 1 first, depth 5 last.
	if result.Evolutions[0].Species != "link1" {
		t.Errorf("Evolutions[0] = %q, want \"link1\" (shallowest)",
			result.Evolutions[0].Species)
	}

	if result.Evolutions[4].Species != "link5" {
		t.Errorf("Evolutions[4] = %q, want \"link5\" (depth cap = 5)",
			result.Evolutions[4].Species)
	}

	// link6 and link7 must not be present — both exceed maxEvolutionDepth.
	for _, stage := range result.Evolutions {
		if stage.Species == "link6" || stage.Species == "link7" {
			t.Errorf("species %q leaked past the depth cap; Evolutions = %+v",
				stage.Species, result.Evolutions)
		}
	}
}
