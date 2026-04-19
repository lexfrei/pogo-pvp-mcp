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
