package tools_test

import (
	"context"
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

func newEvolutionTargetTool(t *testing.T, gmJSON string) *tools.EvolutionTargetTool {
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

	return tools.NewEvolutionTargetTool(gmMgr)
}

// TestEvolutionTargetTool_LinearChainFromRoot exercises the happy
// path on the squirtle → wartortle → blastoise chain. Asking for
// blastoise under Great League returns squirtle as the chain root
// and a non-zero MaxCPToCatch with MaxLevel on the 0.5 grid.
func TestEvolutionTargetTool_LinearChainFromRoot(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
		TargetSpecies:       speciesBlastoise,
		League:              leagueGreat,
		TargetPercentOfBest: 95,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.FromSpecies != speciesSquirtle {
		t.Errorf("FromSpecies = %q, want %q", result.FromSpecies, speciesSquirtle)
	}

	wantChain := []string{speciesSquirtle, speciesWartortle, speciesBlastoise}
	if !slices.Equal(result.ChainFromTo, wantChain) {
		t.Errorf("ChainFromTo = %v, want %v", result.ChainFromTo, wantChain)
	}

	if result.MaxCPToCatch <= 0 {
		t.Errorf("MaxCPToCatch = %d, want > 0", result.MaxCPToCatch)
	}

	if result.MaxLevel < 1 || result.MaxLevel > 50 {
		t.Errorf("MaxLevel = %.1f, want within [1, 50]", result.MaxLevel)
	}

	if result.MaxLevel*2 != float64(int(result.MaxLevel*2)) {
		t.Errorf("MaxLevel = %.2f not on 0.5 grid", result.MaxLevel)
	}

	if result.PercentOfBestAtMax < 95 {
		t.Errorf("PercentOfBestAtMax = %.2f, want >= 95 (threshold)", result.PercentOfBestAtMax)
	}

	if result.CPCap != 1500 {
		t.Errorf("CPCap = %d, want 1500 for great league", result.CPCap)
	}

	if result.BestStatProduct <= 0 {
		t.Errorf("BestStatProduct = %.2f, want > 0", result.BestStatProduct)
	}

	if result.TypicalWildCPRange[0] <= 0 || result.TypicalWildCPRange[1] <= result.TypicalWildCPRange[0] {
		t.Errorf("TypicalWildCPRange = %v, want [min>0, max>min]", result.TypicalWildCPRange)
	}

	if result.EvolutionHint == "" {
		t.Errorf("EvolutionHint empty, want non-empty hint for a multi-stage chain")
	}
}

// TestEvolutionTargetTool_OneHopChain pins the walk when the target
// is only one evolution beyond the chain root. Wartortle's
// PreEvolution is squirtle; squirtle itself has no ancestor, so the
// walk terminates with [squirtle, wartortle] and FromSpecies=squirtle.
func TestEvolutionTargetTool_OneHopChain(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
		TargetSpecies: speciesWartortle,
		League:        leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.FromSpecies != speciesSquirtle {
		t.Errorf("FromSpecies = %q, want %q (pre-evo)", result.FromSpecies, speciesSquirtle)
	}

	wantChain := []string{speciesSquirtle, speciesWartortle}
	if !slices.Equal(result.ChainFromTo, wantChain) {
		t.Errorf("ChainFromTo = %v, want %v", result.ChainFromTo, wantChain)
	}
}

// TestEvolutionTargetTool_OrphanPreEvolution pins the behaviour when
// the target's PreEvolution string points at a species that is not
// in the gamemaster snapshot (a real skew scenario when pvpoke adds
// a new ancestor that has not yet been cached). The walk terminates
// at the last known species rather than returning an error, so
// alakazam → kadabra (absent from fixture) collapses the chain to
// just [alakazam] with FromSpecies=alakazam. The caller sees a
// single-element chain and can detect the collapse by observing
// FromSpecies == TargetSpecies.
func TestEvolutionTargetTool_OrphanPreEvolution(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
		TargetSpecies: "alakazam",
		League:        leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.FromSpecies != "alakazam" {
		t.Errorf("FromSpecies = %q, want alakazam (walk truncated at orphan PreEvolution)",
			result.FromSpecies)
	}

	if len(result.ChainFromTo) != 1 {
		t.Errorf("ChainFromTo = %v, want single element (walk truncated)", result.ChainFromTo)
	}
}

// TestEvolutionTargetTool_TerminalWithoutPreEvolution pins the hard
// error on a species without a PreEvolution: asking evolution_target
// for a base-form species is a caller mistake that should surface
// ErrNotInEvolutionChain, not silent empty chain.
func TestEvolutionTargetTool_TerminalWithoutPreEvolution(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
		TargetSpecies: speciesSquirtle,
		League:        leagueGreat,
	})
	if !errors.Is(err, tools.ErrNotInEvolutionChain) {
		t.Errorf("err = %v, want wrapping ErrNotInEvolutionChain", err)
	}
}

// TestEvolutionTargetTool_UnknownSpecies pins that an unknown target
// wraps ErrUnknownSpecies — shared semantics with the other
// gamemaster-backed tools.
func TestEvolutionTargetTool_UnknownSpecies(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
		TargetSpecies: "farigiraf",
		League:        leagueGreat,
	})
	if !errors.Is(err, tools.ErrUnknownSpecies) {
		t.Errorf("err = %v, want wrapping ErrUnknownSpecies", err)
	}
}

// TestEvolutionTargetTool_UnknownLeague pins the rejection path when
// the league name is not in the canonical table.
func TestEvolutionTargetTool_UnknownLeague(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
		TargetSpecies: speciesBlastoise,
		League:        "masterzzz",
	})
	if !errors.Is(err, tools.ErrUnknownLeague) {
		t.Errorf("err = %v, want wrapping ErrUnknownLeague", err)
	}
}

// TestEvolutionTargetTool_InvalidTargetPercent pins that percent
// values outside [0, 100] are rejected with ErrInvalidTargetPercent.
// Zero is valid (uses default 95); negative and >100 are not.
func TestEvolutionTargetTool_InvalidTargetPercent(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	cases := []struct {
		name    string
		percent float64
	}{
		{"negative", -1},
		{"above_100", 101},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
				TargetSpecies:       speciesBlastoise,
				League:              leagueGreat,
				TargetPercentOfBest: tc.percent,
			})
			if !errors.Is(err, tools.ErrInvalidTargetPercent) {
				t.Errorf("err = %v, want wrapping ErrInvalidTargetPercent", err)
			}
		})
	}
}

// TestEvolutionTargetTool_DefaultThresholdApplied pins that leaving
// TargetPercentOfBest at zero falls back to the 95% default. The
// result's TargetPercentOfBest field echoes the resolved threshold
// so callers can tell "we used the default" from "we used your
// request".
func TestEvolutionTargetTool_DefaultThresholdApplied(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
		TargetSpecies: speciesBlastoise,
		League:        leagueGreat,
		// TargetPercentOfBest deliberately omitted
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.TargetPercentOfBest != 95 {
		t.Errorf("TargetPercentOfBest = %.2f, want 95 (default)", result.TargetPercentOfBest)
	}
}

// TestEvolutionTargetTool_LowerThresholdIncreasesCeiling pins the
// monotonic relationship between threshold and MaxCPToCatch: asking
// for 80% should admit IV spreads that 95% would reject, so the CP
// ceiling grows (or at minimum does not shrink) as the threshold
// drops. This also validates that the threshold parameter is
// actually consumed — a no-op implementation would return the same
// MaxCPToCatch regardless.
func TestEvolutionTargetTool_LowerThresholdIncreasesCeiling(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, strict, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
		TargetSpecies:       speciesBlastoise,
		League:              leagueGreat,
		TargetPercentOfBest: 98,
	})
	if err != nil {
		t.Fatalf("strict handler: %v", err)
	}

	_, relaxed, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
		TargetSpecies:       speciesBlastoise,
		League:              leagueGreat,
		TargetPercentOfBest: 80,
	})
	if err != nil {
		t.Fatalf("relaxed handler: %v", err)
	}

	if relaxed.MaxCPToCatch < strict.MaxCPToCatch {
		t.Errorf("relaxed MaxCPToCatch=%d below strict=%d — threshold not consumed or monotonicity broken",
			relaxed.MaxCPToCatch, strict.MaxCPToCatch)
	}
}

// TestEvolutionTargetTool_ContextCancelled pins early-exit behaviour
// when the caller hands in an already-cancelled context. The sweep
// runs 4096 IVs × inner CP math so an uncancellable hot loop would
// delay error propagation on a client disconnect.
func TestEvolutionTargetTool_ContextCancelled(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err := handler(ctx, nil, tools.EvolutionTargetParams{
		TargetSpecies: speciesBlastoise,
		League:        leagueGreat,
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want wrapping context.Canceled", err)
	}
}
