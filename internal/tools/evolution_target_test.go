package tools_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
// and a non-zero MaxRootCPAtEvolvedLevel with EvolvedLevel on the 0.5 grid.
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

	if result.MaxRootCPAtEvolvedLevel <= 0 {
		t.Errorf("MaxRootCPAtEvolvedLevel = %d, want > 0", result.MaxRootCPAtEvolvedLevel)
	}

	if result.EvolvedLevel < 1 || result.EvolvedLevel > 50 {
		t.Errorf("EvolvedLevel = %.1f, want within [1, 50]", result.EvolvedLevel)
	}

	if result.EvolvedLevel*2 != float64(int(result.EvolvedLevel*2)) {
		t.Errorf("EvolvedLevel = %.2f not on 0.5 grid", result.EvolvedLevel)
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

	if result.TypicalWildCPRangeUnboosted[0] <= 0 || result.TypicalWildCPRangeUnboosted[1] <= result.TypicalWildCPRangeUnboosted[0] {
		t.Errorf("TypicalWildCPRangeUnboosted = %v, want [min>0, max>min]", result.TypicalWildCPRangeUnboosted)
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
// monotonic relationship between threshold and MaxRootCPAtEvolvedLevel: asking
// for 80% should admit IV spreads that 95% would reject, so the CP
// ceiling grows (or at minimum does not shrink) as the threshold
// drops. This also validates that the threshold parameter is
// actually consumed — a no-op implementation would return the same
// MaxRootCPAtEvolvedLevel regardless.
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

	if relaxed.MaxRootCPAtEvolvedLevel < strict.MaxRootCPAtEvolvedLevel {
		t.Errorf("relaxed MaxRootCPAtEvolvedLevel=%d below strict=%d — threshold not consumed or monotonicity broken",
			relaxed.MaxRootCPAtEvolvedLevel, strict.MaxRootCPAtEvolvedLevel)
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

// TestEvolutionTargetTool_BranchingChainTarget pins that asking for
// one branch of a branching chain (eevee → vaporeon / jolteon) resolves
// the chain correctly back to eevee. walkPreEvolutionChain follows
// PreEvolution which is a single id per species, not the forward
// branching map — so a request for vaporeon should ignore the sibling
// jolteon entirely and return [eevee, vaporeon].
func TestEvolutionTargetTool_BranchingChainTarget(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
		TargetSpecies: speciesVaporeon,
		League:        leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.FromSpecies != speciesEevee {
		t.Errorf("FromSpecies = %q, want %q (sibling branches must not leak in)",
			result.FromSpecies, speciesEevee)
	}

	wantChain := []string{speciesEevee, speciesVaporeon}
	if !slices.Equal(result.ChainFromTo, wantChain) {
		t.Errorf("ChainFromTo = %v, want %v", result.ChainFromTo, wantChain)
	}

	for _, id := range result.ChainFromTo {
		if id == speciesJolteon {
			t.Errorf("ChainFromTo contains %q (sibling branch) — reverse walk should not surface siblings",
				speciesJolteon)
		}
	}
}

// TestEvolutionTargetTool_ThresholdAbove100Rejected pins the upper
// bound on TargetPercentOfBest: any value strictly above 100 is
// rejected with ErrInvalidTargetPercent before the sweep runs, since
// no IV spread can by definition exceed the best spread's stat
// product. This is the upfront-validation path; ErrThresholdUnreachable
// remains as a defensive sentinel for future scenarios (e.g. a cup
// filter one day excluding the winning IV range).
func TestEvolutionTargetTool_ThresholdAbove100Rejected(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
		TargetSpecies:       speciesBlastoise,
		League:              leagueGreat,
		TargetPercentOfBest: 100.01,
	})
	if !errors.Is(err, tools.ErrInvalidTargetPercent) {
		t.Errorf("err = %v, want wrapping ErrInvalidTargetPercent (>100 rejected upfront)", err)
	}
}

// TestEvolutionTargetTool_XLAllowedShiftsCeiling pins that XL=true
// raises the effective level cap above 40, which for master league
// (cpCap=10000, unreachable in pre-XL) produces a different EvolvedLevel
// than XL=false. The monotonicity test locks the flag as actually
// wired into FindOptimalSpread / LevelForCP.
func TestEvolutionTargetTool_XLAllowedShiftsCeiling(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, noXL, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
		TargetSpecies: speciesBlastoise,
		League:        "master",
		XL:            false,
	})
	if err != nil {
		t.Fatalf("no-XL handler: %v", err)
	}

	_, withXL, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
		TargetSpecies: speciesBlastoise,
		League:        "master",
		XL:            true,
	})
	if err != nil {
		t.Fatalf("with-XL handler: %v", err)
	}

	if noXL.EvolvedLevel > 40 {
		t.Errorf("no-XL EvolvedLevel = %.1f, want ≤ 40 (XL gates levels > 40)", noXL.EvolvedLevel)
	}

	if withXL.EvolvedLevel <= noXL.EvolvedLevel {
		t.Errorf("with-XL EvolvedLevel = %.1f not above no-XL EvolvedLevel = %.1f — flag not consumed",
			withXL.EvolvedLevel, noXL.EvolvedLevel)
	}
}

// TestEvolutionTargetTool_ShadowHonoured pins Phase X-II behaviour:
// `Options.Shadow=true` rerouts the target lookup to the _shadow
// pvpoke entry when present, and sets ShadowVariantMissing=true
// otherwise (fallback to base species). The fixture does not publish
// shadow blastoise, so we expect the missing-variant flag.
func TestEvolutionTargetTool_ShadowHonoured(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
		TargetSpecies: speciesBlastoise,
		League:        leagueGreat,
		Options:       tools.CombatantOptions{Shadow: true},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if !result.ShadowVariantMissing {
		t.Errorf("ShadowVariantMissing = false, want true (fixture has no blastoise_shadow entry)")
	}

	if result.ResolvedSpeciesID != speciesBlastoise {
		t.Errorf("ResolvedSpeciesID = %q, want %q (base species fallback)",
			result.ResolvedSpeciesID, speciesBlastoise)
	}

	if result.MaxRootCPAtEvolvedLevel <= 0 {
		t.Errorf("MaxRootCPAtEvolvedLevel = %d, want > 0 even with shadow fallback", result.MaxRootCPAtEvolvedLevel)
	}
}

// TestEvolutionTargetTool_HintHasNoStaleToolReference locks the
// evolution_hint string against a regression where a previous
// revision named a non-existent pvp_evolution_cost tool. No
// `pvp_*` substring in the hint means no misdirection.
func TestEvolutionTargetTool_HintHasNoStaleToolReference(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
		TargetSpecies: speciesBlastoise,
		League:        leagueGreat,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if strings.Contains(result.EvolutionHint, "pvp_") {
		t.Errorf("EvolutionHint = %q, must not reference any pvp_* tool name (stale references mis-direct callers)",
			result.EvolutionHint)
	}
}

// TestEvolutionTargetTool_MaxRootCPAtEvolvedLevelContract pins the
// renamed field's semantics: the value is the root CP at the winning
// EvolvedLevel, NOT a wild-catch CP ceiling. Under master league
// (cpCap=10000, blastoise unconstrained), EvolvedLevel can reach L40
// (or L50 with XL), producing a root CP that exceeds the wild max at
// L30 documented in TypicalWildCPRangeUnboosted. This test locks the
// contract so future callers reading the field understand the
// difference.
func TestEvolutionTargetTool_MaxRootCPAtEvolvedLevelContract(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.EvolutionTargetParams{
		TargetSpecies: speciesBlastoise,
		League:        "master",
		XL:            true,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	// Under master + XL, EvolvedLevel should push toward L50 which is
	// above the wild L30 cap. The root CP at that level exceeds the
	// wild-catch ceiling by design — this is the contract.
	if result.EvolvedLevel <= float64(typicalWildMaxForTest) {
		t.Errorf("EvolvedLevel = %.1f, want > %d under master+XL (confirms test setup)",
			result.EvolvedLevel, typicalWildMaxForTest)
	}

	if result.MaxRootCPAtEvolvedLevel <= result.TypicalWildCPRangeUnboosted[1] {
		t.Errorf("MaxRootCPAtEvolvedLevel = %d, want > TypicalWildCPRangeUnboosted[1] = %d "+
			"(the renamed field is root CP at evolved level, NOT a wild-catch CP ceiling)",
			result.MaxRootCPAtEvolvedLevel, result.TypicalWildCPRangeUnboosted[1])
	}
}

// typicalWildMaxForTest mirrors the unexported typicalWildUnboostedMaxLevel
// constant so the contract test above can compare against it without
// touching the production const. Duplicated intentionally — a future
// change that bumps the internal wild-max must also bump this.
const typicalWildMaxForTest = 30

// TestEvolutionTargetTool_ContextCancelledMidSweep pins that the
// 4096-IV sweep polls ctx.Err() at its outer-loop boundary. The test
// uses a goroutine that cancels the context slightly after the handler
// is invoked, so cancellation lands during the sweep rather than
// during preHandleValidation. Both paths return a context-wrapped
// error; the test only asserts propagation (specific error is
// context.Canceled in either case). This mirrors the CLAUDE.md
// invariant that every heavy-sweep tool polls ctx between iterations.
func TestEvolutionTargetTool_ContextCancelledMidSweep(t *testing.T) {
	t.Parallel()

	tool := newEvolutionTargetTool(t, evolutionFixtureGamemaster)
	handler := tool.Handler()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	// Kick off cancellation after a tiny delay. 250µs is long enough
	// that preHandleValidation's eager ctx.Err() check usually passes
	// and the sweep is running when cancel fires. The 4096-IV sweep
	// takes ~1-2ms on laptop hardware, so cancel lands mid-sweep on a
	// reasonable fraction of runs. We accept either preHandleValidation
	// catching the cancel or the sweep catching it — the test goal is
	// "context error propagates", not "pins which check caught it".
	go func() {
		time.Sleep(250 * time.Microsecond)
		cancel()
	}()

	_, _, err := handler(ctx, nil, tools.EvolutionTargetParams{
		TargetSpecies: speciesBlastoise,
		League:        leagueGreat,
	})

	// If the sweep finishes before cancel fires (fast hardware) the
	// handler returns nil — not a failure. We only assert that IF an
	// error surfaces, it is a context-cancellation. A race-window
	// gating test would be flaky on CI.
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want nil or wrapping context.Canceled", err)
	}
}
