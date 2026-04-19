package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrNotInEvolutionChain is returned when EvolutionTargetParams.TargetSpecies
// resolves to a species without a PreEvolution — there is no ancestor
// to catch, so the reverse-lookup has no answer.
var ErrNotInEvolutionChain = errors.New("target species has no pre-evolution")

// ErrInvalidTargetPercent is returned when TargetPercentOfBest is
// outside (0, 100]. Zero is treated as "use the default" so it does
// not surface here; a negative value or >100 is rejected.
var ErrInvalidTargetPercent = errors.New("invalid target_percent_of_best")

// ErrThresholdUnreachable is returned when no IV spread produces a
// target stat product clearing TargetPercentOfBest. Usually means
// the species is too weak for the league cap (rare; FindOptimalSpread
// itself would find SOME best spread) or the threshold is
// ≥100% + floating-point noise.
var ErrThresholdUnreachable = errors.New("no IV spread clears the requested percent-of-best threshold")

// defaultTargetPercentOfBest matches the plan: 95% of the best
// stat-product spread is the canonical "near-hundo" threshold Pokémon
// GO players use when deciding whether to power up a candidate.
const defaultTargetPercentOfBest = 95.0

// maxPreEvolutionDepth caps the backward walk to a sensible ceiling so
// a malformed gamemaster cycle (pvpoke data being the ultimate source
// of truth for the chain) cannot spin the helper forever. Real Pokémon
// GO chains are at most two hops (base → mid → final); five matches
// the forward BFS cap used in evolution_preview / team_builder_evolve.
const maxPreEvolutionDepth = 5

// typicalWildUnboostedMaxLevel is the wild-spawn ceiling used in the
// "typical_wild_cp_range" advisory. Wild spawns cap at level 30 when
// not weather-boosted; this mirrors encounter_cp_range's
// encounterWildUnboostedMaxLevel but is duplicated here so this tool
// does not couple to the encounter-rule table ordering.
const typicalWildUnboostedMaxLevel = 30

// EvolutionTargetParams is the JSON input for pvp_evolution_target.
// The caller supplies the DESIRED final species and a league; the tool
// walks PreEvolution back to the chain root and reports how high the
// wild-catch CP can be before powering up the evolved form busts the
// desired percent-of-best threshold.
//
// Cup is accepted for API symmetry but does not change the computation
// — cup filters restrict which species are legal, not the IV/level
// math that drives the threshold. A future enhancement could pre-check
// the target fits the cup's include/exclude block.
type EvolutionTargetParams struct {
	TargetSpecies       string           `json:"target_species" jsonschema:"the desired evolved species id"`
	League              string           `json:"league" jsonschema:"great, ultra, master, or little"`
	Cup                 string           `json:"cup,omitempty" jsonschema:"optional cup id; accepted but not enforced"`
	TargetPercentOfBest float64          `json:"target_percent_of_best,omitempty" jsonschema:"stat-product threshold; 0..100 (default 95)"`
	XL                  bool             `json:"xl,omitempty" jsonschema:"allow XL candy levels above 40"`
	Options             CombatantOptions `json:"options,omitzero" jsonschema:"shadow / lucky / purified flags"`
}

// EvolutionTargetResult is the JSON output.
//
// MaxRootCPAtEvolvedLevel is the CP the chain-root species WOULD
// DISPLAY if it were at the winning (IV, level) pair — i.e. AFTER
// the caller has powered it up to EvolvedLevel. It is NOT clamped
// to wild-catch levels; for leagues whose optimal level exceeds the
// wild ceiling (Ultra L40+, Master L50 with XL) this value will
// exceed anything a freshly-caught root species can display. Use
// TypicalWildCPRangeUnboosted to bound the realistic wild-catch CP
// space and compare against the current catch's actual CP on the
// caller side.
//
// EvolvedLevel is the 0.5-grid level of the winning spread. Level
// preserves across evolution in Pokémon GO so the target inherits
// this exact level after evolving.
//
// PercentOfBestAtMax echoes the actual stat-product fraction
// achieved by the winning (IV, level) spread (always ≥ threshold
// by construction).
type EvolutionTargetResult struct {
	TargetSpecies               string   `json:"target_species"`
	ResolvedSpeciesID           string   `json:"resolved_species_id,omitempty"`
	FromSpecies                 string   `json:"from_species"`
	ChainFromTo                 []string `json:"chain_from_to"`
	League                      string   `json:"league"`
	Cup                         string   `json:"cup,omitempty"`
	CPCap                       int      `json:"cp_cap"`
	TargetPercentOfBest         float64  `json:"target_percent_of_best"`
	MaxRootCPAtEvolvedLevel     int      `json:"max_root_cp_at_evolved_level"`
	EvolvedLevel                float64  `json:"evolved_level"`
	TypicalWildCPRangeUnboosted [2]int   `json:"typical_wild_cp_range_unboosted"`
	PercentOfBestAtMax          float64  `json:"percent_of_best_at_max"`
	EvolutionHint               string   `json:"evolution_hint"`
	BestStatProduct             float64  `json:"best_stat_product"`
	ShadowVariantMissing        bool     `json:"shadow_variant_missing,omitempty"`
}

// EvolutionTargetTool wraps the gamemaster manager.
type EvolutionTargetTool struct {
	manager *gamemaster.Manager
}

// NewEvolutionTargetTool constructs the tool bound to the gamemaster.
func NewEvolutionTargetTool(manager *gamemaster.Manager) *EvolutionTargetTool {
	return &EvolutionTargetTool{manager: manager}
}

const evolutionTargetToolDescription = "Reverse-lookup for powerup planning: given the DESIRED evolved species " +
	"and a league, walk PreEvolution back to the chain root and report the maximum CP of a wild root-species " +
	"catch such that at least one IV spread evolves up to a target whose stat product clears " +
	"target_percent_of_best of the best legal spread under the league cap. Default threshold 95%."

// Tool returns the MCP tool registration.
func (*EvolutionTargetTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_evolution_target",
		Description: evolutionTargetToolDescription,
	}
}

// Handler returns the MCP-typed handler function.
func (tool *EvolutionTargetTool) Handler() mcp.ToolHandlerFor[EvolutionTargetParams, EvolutionTargetResult] {
	return tool.handle
}

// handle orchestrates the reverse-lookup. It resolves the target
// species (with shadow-aware lookup), walks PreEvolution back to the
// chain root, computes the best stat product for the target under the
// league cap, then iterates every IV spread to find the one producing
// the maximum root-species CP while still clearing the threshold.
func (tool *EvolutionTargetTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params EvolutionTargetParams,
) (*mcp.CallToolResult, EvolutionTargetResult, error) {
	inputs, err := tool.preHandleValidation(ctx, &params)
	if err != nil {
		return nil, EvolutionTargetResult{}, err
	}

	best, err := pogopvp.FindOptimalSpread(
		inputs.target.BaseStats, inputs.cpCap,
		pogopvp.FindSpreadOpts{XLAllowed: params.XL})
	if err != nil {
		return nil, EvolutionTargetResult{},
			fmt.Errorf("target %q best spread: %w", params.TargetSpecies, err)
	}

	threshold := params.TargetPercentOfBest
	if threshold == 0 {
		threshold = defaultTargetPercentOfBest
	}

	ceiling, err := searchEvolutionTargetCeiling(ctx, inputs, best, threshold, params.XL)
	if err != nil {
		return nil, EvolutionTargetResult{}, err
	}

	if ceiling.MaxRootCPAtEvolvedLevel == 0 {
		return nil, EvolutionTargetResult{}, fmt.Errorf(
			"%w: target %q under cpCap=%d at threshold %.1f%%",
			ErrThresholdUnreachable, inputs.resolvedTargetID, inputs.cpCap, threshold)
	}

	return nil, buildEvolutionTargetResult(&params, inputs, best, threshold, ceiling), nil
}

// buildEvolutionTargetResult projects the resolved inputs + search
// candidate into the JSON output shape. Extracted from handle so the
// outer function stays under the funlen budget.
func buildEvolutionTargetResult(
	params *EvolutionTargetParams, inputs *evolutionTargetInputs,
	best pogopvp.OptimalSpread, threshold float64, ceiling searchCandidate,
) EvolutionTargetResult {
	return EvolutionTargetResult{
		TargetSpecies:               params.TargetSpecies,
		ResolvedSpeciesID:           inputs.resolvedTargetID,
		FromSpecies:                 inputs.root.ID,
		ChainFromTo:                 inputs.chain,
		League:                      params.League,
		Cup:                         params.Cup,
		CPCap:                       inputs.cpCap,
		TargetPercentOfBest:         threshold,
		MaxRootCPAtEvolvedLevel:     ceiling.MaxRootCPAtEvolvedLevel,
		EvolvedLevel:                ceiling.EvolvedLevel,
		TypicalWildCPRangeUnboosted: typicalWildCPRangeFor(inputs.root),
		PercentOfBestAtMax:          ceiling.PercentOfBestAtMax,
		EvolutionHint:               evolutionHintFor(inputs.chain),
		BestStatProduct:             best.StatProduct,
		ShadowVariantMissing:        inputs.shadowMissing,
	}
}

// evolutionTargetInputs bundles the resolved values common to both the
// precondition step and the ceiling search so handle stays under the
// funlen budget.
type evolutionTargetInputs struct {
	target           *pogopvp.Species
	root             *pogopvp.Species
	chain            []string
	cpCap            int
	resolvedTargetID string
	shadowMissing    bool
}

// preHandleValidation owns the cheap precondition work: context
// cancel, snapshot fetch, shadow-aware target lookup, percent
// validation, league → cpCap resolution, and PreEvolution walk.
func (tool *EvolutionTargetTool) preHandleValidation(
	ctx context.Context, params *EvolutionTargetParams,
) (*evolutionTargetInputs, error) {
	err := ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("evolution_target cancelled: %w", err)
	}

	if params.TargetPercentOfBest < 0 || params.TargetPercentOfBest > 100 {
		return nil, fmt.Errorf("%w: %.2f", ErrInvalidTargetPercent, params.TargetPercentOfBest)
	}

	snapshot := tool.manager.Current()
	if snapshot == nil {
		return nil, ErrGamemasterNotLoaded
	}

	target, resolvedID, shadowMissing, ok := resolveSpeciesLookup(
		snapshot, params.TargetSpecies, params.Options)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownSpecies, params.TargetSpecies)
	}

	if target.PreEvolution == "" {
		return nil, fmt.Errorf("%w: %q has no pre-evolution", ErrNotInEvolutionChain, target.ID)
	}

	cpCap, err := resolveCPCap(params.League, 0)
	if err != nil {
		return nil, err
	}

	root, chain := walkPreEvolutionChain(snapshot, &target)

	return &evolutionTargetInputs{
		target:           &target,
		root:             root,
		chain:            chain,
		cpCap:            cpCap,
		resolvedTargetID: resolvedID,
		shadowMissing:    shadowMissing,
	}, nil
}

// walkPreEvolutionChain follows PreEvolution backward from target up
// to the chain root (PreEvolution == ""). Returns the root species
// pointer plus an ordered [root, mid, ..., target] chain of species
// ids. Stops at maxPreEvolutionDepth as a malformed-data safeguard —
// real chains are at most three entries long.
//
//nolint:gocritic // unnamedResult: (root species, id chain) order documented in godoc
func walkPreEvolutionChain(
	snapshot *pogopvp.Gamemaster, target *pogopvp.Species,
) (*pogopvp.Species, []string) {
	reversed := []string{target.ID}
	current := target

	for range maxPreEvolutionDepth {
		if current.PreEvolution == "" {
			break
		}

		parent, ok := snapshot.Pokemon[current.PreEvolution]
		if !ok {
			break
		}

		reversed = append(reversed, parent.ID)
		current = &parent
	}

	chain := make([]string, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		chain = append(chain, reversed[i])
	}

	return current, chain
}

// searchCandidate is the running best-so-far during the IV sweep.
type searchCandidate struct {
	MaxRootCPAtEvolvedLevel int
	EvolvedLevel            float64
	PercentOfBestAtMax      float64
}

// searchEvolutionTargetCeiling iterates every IV spread in [0, MaxIV]^3.
// For each, it finds the highest target level fitting cpCap, computes
// the percent-of-best, and — when the threshold is cleared — computes
// the root-species CP at that same shared level (evolution preserves
// level in Pokémon GO). The winner is the (IV, level) pair producing
// the greatest root-species CP: the CP ceiling for wild catches.
//
// ctx.Err() is polled at the outer-loop boundary so a client
// disconnect during the 4096-IV sweep releases the handler
// goroutine promptly, matching the ctx-polling discipline documented
// in CLAUDE.md for every other heavy-sweep tool (team_builder,
// counter_finder, threat_coverage, rank_batch).
//
// If no IV spread clears the threshold the zero-valued candidate is
// returned with a nil error; the caller surfaces
// ErrThresholdUnreachable.
func searchEvolutionTargetCeiling(
	ctx context.Context, inputs *evolutionTargetInputs, best pogopvp.OptimalSpread,
	thresholdPercent float64, xlAllowed bool,
) (searchCandidate, error) {
	var candidate searchCandidate

	opts := pogopvp.FindSpreadOpts{XLAllowed: xlAllowed}

	for atk := 0; atk <= pogopvp.MaxIV; atk++ {
		err := ctx.Err()
		if err != nil {
			return searchCandidate{}, fmt.Errorf("evolution_target cancelled mid-sweep: %w", err)
		}

		for def := 0; def <= pogopvp.MaxIV; def++ {
			for sta := 0; sta <= pogopvp.MaxIV; sta++ {
				evaluateIVCandidate(inputs, atk, def, sta, best, thresholdPercent, opts, &candidate)
			}
		}
	}

	return candidate, nil
}

// evaluateIVCandidate is the per-IV body of the ceiling search.
// Extracted from searchEvolutionTargetCeiling so the triple-nested
// loop stays readable and gocognit is happy with the outer function.
// Mutates *candidate in place when a new best is found.
func evaluateIVCandidate(
	inputs *evolutionTargetInputs, atk, def, sta int,
	best pogopvp.OptimalSpread, thresholdPercent float64,
	opts pogopvp.FindSpreadOpts, candidate *searchCandidate,
) {
	ivs, err := pogopvp.NewIV(atk, def, sta)
	if err != nil {
		return
	}

	level, sp, ok := targetFitAndStatProduct(inputs.target, ivs, inputs.cpCap, opts)
	if !ok {
		return
	}

	pct := sp / best.StatProduct * 100
	if pct < thresholdPercent {
		return
	}

	cpm, err := pogopvp.CPMAt(level)
	if err != nil {
		return
	}

	rootCP := pogopvp.ComputeCP(inputs.root.BaseStats, ivs, cpm)
	if rootCP <= candidate.MaxRootCPAtEvolvedLevel {
		return
	}

	*candidate = searchCandidate{
		MaxRootCPAtEvolvedLevel: rootCP,
		EvolvedLevel:            level,
		PercentOfBestAtMax:      pct,
	}
}

// targetFitAndStatProduct returns (level, statProduct, true) for the
// highest 0.5-grid level at which target with these IVs fits cpCap.
// Any engine error (CP too low, malformed opts, unreachable CPM) maps
// to ok=false so the caller drops this IV candidate.
//
//nolint:gocritic // unnamedResult: (level, statProduct, ok) documented in godoc
func targetFitAndStatProduct(
	target *pogopvp.Species, ivs pogopvp.IV, cpCap int, opts pogopvp.FindSpreadOpts,
) (float64, float64, bool) {
	res, err := pogopvp.LevelForCP(target.BaseStats, ivs, cpCap, opts)
	if err != nil {
		return 0, 0, false
	}

	cpm, err := pogopvp.CPMAt(res.Level)
	if err != nil {
		return 0, 0, false
	}

	stats := pogopvp.ComputeStats(target.BaseStats, ivs, cpm)
	sp := pogopvp.ComputeStatProduct(stats)

	return res.Level, sp, true
}

// typicalWildCPRangeFor is the advisory range a trainer would see on
// an unboosted wild spawn: [0/0/0 IV at L1, 15/15/15 IV at L30]. The
// values are produced from the same engine primitives encounter_cp_
// range uses, so they stay in sync by construction.
func typicalWildCPRangeFor(root *pogopvp.Species) [2]int {
	lowIV, err := pogopvp.NewIV(0, 0, 0)
	if err != nil {
		return [2]int{0, 0}
	}

	highIV, err := pogopvp.NewIV(pogopvp.MaxIV, pogopvp.MaxIV, pogopvp.MaxIV)
	if err != nil {
		return [2]int{0, 0}
	}

	lowCPM, err := pogopvp.CPMAt(pogopvp.MinLevel)
	if err != nil {
		return [2]int{0, 0}
	}

	highCPM, err := pogopvp.CPMAt(typicalWildUnboostedMaxLevel)
	if err != nil {
		return [2]int{0, 0}
	}

	lowCP := pogopvp.ComputeCP(root.BaseStats, lowIV, lowCPM)
	highCP := pogopvp.ComputeCP(root.BaseStats, highIV, highCPM)

	return [2]int{lowCP, highCP}
}

// evolutionHintFor is a best-effort human-readable note describing
// how to evolve along the chain. Deliberately does NOT reference any
// specific tool name — a previous revision pointed callers at a
// non-existent pvp_evolution_cost tool, actively mis-directing them
// on every successful response. Per-step candy / item data is not
// in scope here; callers should consult their preferred data source
// (Bulbapedia, PokéMiners, in-game display) for those numbers.
// Chains of length one (target == root, impossible here thanks to
// the PreEvolution gate) get an empty string.
func evolutionHintFor(chain []string) string {
	if len(chain) < 2 {
		return ""
	}

	return "Catch " + chain[0] + " and evolve up the chain (" +
		formatChainArrow(chain) + ")."
}

// formatChainArrow renders [a, b, c] as "a → b → c". Duplicated here
// (instead of pulling in strings.Join with a separator) so the arrow
// glyph is consistent with the rest of the MCP output.
func formatChainArrow(chain []string) string {
	if len(chain) == 0 {
		return ""
	}

	var builder strings.Builder

	builder.WriteString(chain[0])

	for _, id := range chain[1:] {
		builder.WriteString(" → ")
		builder.WriteString(id)
	}

	return builder.String()
}
