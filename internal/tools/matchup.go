package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

// CombatantOptions carries per-Pokémon modifier flags that affect
// either combat resolution (shadow form uses a distinct pvpoke
// entry with shadow-exclusive legacy moves, rankings, and recommended
// moveset) or cost estimation (shadow ×1.2, purified ×0.9, lucky
// ×0.5 on stardust-only). Flags are orthogonal — a Shadow Pokémon
// is never Purified, but Lucky can stack with either.
//
// The `_shadow` suffix on species ids is no longer the convention
// for signalling shadow form — set Shadow=true in Options and leave
// Species as the base id (e.g. "medicham"). The tool internally
// resolves to the shadow gamemaster entry via "_shadow" suffix
// concatenation; if pvpoke does not publish a dedicated entry,
// the fallback reports the base species with a shadow_variant_missing
// flag so the caller knows shadow-specific legacy moves / rankings
// were not applied.
//
// Shadow ATK / DEF multipliers: the battle simulator applies the
// in-game ATK × 1.2 / DEF ÷ 1.2 adjustment (HP unchanged) when
// Options.Shadow=true via pogopvp.Combatant.IsShadow. This lands
// verbatim on damage-dealt and damage-received calculations, so
// a shadow matchup produces different HP / turn counts from the
// non-shadow mirror as Niantic intends. Dual-convention tolerance:
// the old species-id "_shadow" suffix also flips IsShadow on
// buildEngineCombatant (see that helper's IsShadow assignment),
// so a caller passing {Species: "medicham_shadow"} without
// Options.Shadow gets the same simulator behaviour as
// {Species: "medicham", Options: {Shadow: true}}.
//
// ShadowVariantMissing + Options.Shadow=true: when pvpoke has not
// yet published a dedicated "_shadow" gamemaster entry for the
// species, resolveSpeciesLookup falls back to the base row and
// sets shadow_variant_missing=true. The simulator STILL applies
// the ATK×1.2 / DEF÷1.2 multipliers to the base stats — the
// caller explicitly asked for shadow behaviour, so the user-intent
// signal outranks pvpoke's data-completeness signal. The missing-
// variant flag is the caller's cue that shadow-specific legacy
// moves / rankings were not consulted; combat damage is still
// simulated as shadow.
type CombatantOptions struct {
	Shadow   bool `json:"shadow,omitempty" jsonschema:"shadow form (×1.2 cost; simulator applies ATK×1.2/DEF÷1.2 in-game multipliers)"`
	Lucky    bool `json:"lucky,omitempty" jsonschema:"lucky Pokémon (×0.5 powerup stardust only)"`
	Purified bool `json:"purified,omitempty" jsonschema:"purified form (×0.9 stardust and candy costs)"`
}

// Combatant is the MCP-visible input shape for one fighter in a
// matchup / team. Shields on matchup is specified once per side at
// the MatchupParams level so callers can sweep (0, 1, 2)×(0, 1, 2)
// outside the tool. FastMove and ChargedMoves are optional: if left
// empty the tool resolves the recommended moveset from the relevant
// (cup, cap) rankings entry. An explicit move overrides the default.
// Options carries modifier flags (shadow / lucky / purified) — see
// CombatantOptions godoc.
//
// Unexported fields (resolvedSpeciesID, shadowVariantMissing,
// autoEvolvedFrom, autoEvolveSkip) are runtime-only bookkeeping
// populated by buildEngineCombatant (shadow-aware lookup) and
// autoEvolvePool (Phase R4.4 team_builder helper); they skip JSON
// (un)marshalling and are read back by resolvedFromSpec /
// autoEvolveFlagsFor / filterPool to surface missing-variant
// signals, auto-evolve provenance flags, and the original-species
// ban match in downstream pipeline stages.
type Combatant struct {
	Species      string           `json:"species" jsonschema:"species id"`
	IV           [3]int           `json:"iv" jsonschema:"IV triple [atk, def, sta]"`
	Level        float64          `json:"level" jsonschema:"level on 0.5 grid, [1.0, 51.0]"`
	FastMove     string           `json:"fast_move,omitempty" jsonschema:"fast move id; omit to use recommended"`
	ChargedMoves []string         `json:"charged_moves,omitempty" jsonschema:"charged move ids; omit to use recommended"`
	Options      CombatantOptions `json:"options,omitzero" jsonschema:"modifier flags: shadow/lucky/purified"`

	resolvedSpeciesID      string
	shadowVariantMissing   bool
	autoEvolvedFrom        string
	autoEvolveSkip         string
	autoEvolveAlternatives []EvolveAlternative
	// originalIndex records the 0-based position this entry held in
	// the caller's input pool before any auto-evolve / filter /
	// ban pass mutated it. Stamped on a COPY of the caller's pool
	// inside team_builder.handle so concurrent parallel-subtests
	// that share a TeamBuilderParams struct do not race on shared
	// backing memory (see R5 finding #6 — earlier implementation
	// mutated caller input and tripped -race detector).
	originalIndex int
}

// ResolvedCombatant echoes back the species + moveset actually used
// by the engine after any recommended-moveset defaulting. Options
// round-trips so the caller sees the modifier flags the tool saw.
// ResolvedSpeciesID reports the underlying pvpoke gamemaster id the
// engine actually queried (e.g. "medicham_shadow" when Options.Shadow
// is true and pvpoke publishes a dedicated shadow entry).
// ShadowVariantMissing is true when Options.Shadow was set but
// pvpoke does not publish a corresponding "_shadow" entry — the
// tool fell back to the base species and did NOT apply shadow-
// specific legacy moves or rankings. Callers should treat results
// on such a combatant as approximate.
type ResolvedCombatant struct {
	Species              string           `json:"species"`
	ResolvedSpeciesID    string           `json:"resolved_species_id,omitempty"`
	FastMove             string           `json:"fast_move"`
	ChargedMoves         []string         `json:"charged_moves"`
	Options              CombatantOptions `json:"options,omitzero"`
	ShadowVariantMissing bool             `json:"shadow_variant_missing,omitempty"`
}

// resolvedFromSpec projects a finalised Combatant (after moveset
// defaulting) to its echo shape, carrying the runtime shadow-lookup
// result populated by buildEngineCombatant.
func resolvedFromSpec(spec *Combatant) ResolvedCombatant {
	charged := make([]string, len(spec.ChargedMoves))
	copy(charged, spec.ChargedMoves)

	return ResolvedCombatant{
		Species:              spec.Species,
		ResolvedSpeciesID:    spec.resolvedSpeciesID,
		FastMove:             spec.FastMove,
		ChargedMoves:         charged,
		Options:              spec.Options,
		ShadowVariantMissing: spec.shadowVariantMissing,
	}
}

// resolveSpeciesForSpec wraps resolveSpeciesLookup for the common
// buildEngineCombatant / rejectLegacy case: on success it populates
// the runtime shadow-resolve fields on the Combatant so the
// response can echo ResolvedSpeciesID / ShadowVariantMissing.
func resolveSpeciesForSpec(
	snapshot *pogopvp.Gamemaster, spec *Combatant,
) (pogopvp.Species, error) {
	species, resolvedID, shadowMissing, ok := resolveSpeciesLookup(
		snapshot, spec.Species, spec.Options)
	if !ok {
		return pogopvp.Species{}, fmt.Errorf("%w: %q", ErrUnknownSpecies, spec.Species)
	}

	spec.resolvedSpeciesID, spec.shadowVariantMissing = resolvedID, shadowMissing

	return species, nil
}

// shadowSuffix is the pvpoke convention for shadow-form species
// ids in the gamemaster entry map. Clients don't emit this suffix
// anymore — they set Options.Shadow=true and let the tool resolve
// the pvpoke entry internally. Kept as a package-private constant
// so the one place that does the concatenation stays greppable.
const shadowSuffix = "_shadow"

// resolveSpeciesLookup performs shadow-aware species lookup against
// the gamemaster snapshot:
//
//   - Options.Shadow=false: direct lookup by baseID.
//   - Options.Shadow=true: try baseID+"_shadow" first; if pvpoke
//     publishes no dedicated shadow entry (rare but possible for
//     shadow-eligible-but-shadow-variant-not-yet-published species),
//     fall back to baseID and report shadowMissing=true so callers
//     can surface the approximation to the end user.
//
// Dual-convention tolerance: if Options.Shadow=true AND baseID
// already ends in "_shadow" the suffix is stripped before the
// lookup. Clients mixing the old suffix convention with the new
// flag would otherwise get a misleading shadow_variant_missing=true
// against their already-shadow species id (the lookup would chase
// "medicham_shadow_shadow"). Stripping is the forgiving option —
// the strict alternative would be a dedicated error, but the wire
// semantics are unambiguous so we just normalise.
//
// resolvedID is the pvpoke key actually used. ok=false means the
// base species id doesn't exist at all, which is an input error the
// caller should wrap into ErrUnknownSpecies.
//
//nolint:gocritic // unnamedResult: (species, resolvedID, shadowMissing, ok) documented in godoc
func resolveSpeciesLookup(
	snapshot *pogopvp.Gamemaster, baseID string, opts CombatantOptions,
) (pogopvp.Species, string, bool, bool) {
	if opts.Shadow {
		baseID = strings.TrimSuffix(baseID, shadowSuffix)

		shadowID := baseID + shadowSuffix
		if species, found := snapshot.Pokemon[shadowID]; found {
			return species, shadowID, false, true
		}
		// Shadow requested but pvpoke has no dedicated entry: fall
		// back to the base species but flag the approximation.
		species, found := snapshot.Pokemon[baseID]
		if !found {
			return pogopvp.Species{}, "", false, false
		}

		return species, baseID, true, true
	}

	species, ok := snapshot.Pokemon[baseID]
	if !ok {
		return pogopvp.Species{}, "", false, false
	}

	return species, baseID, false, true
}

// speciesExists is a predicate wrapper over resolveSpeciesLookup for
// call sites (pvp_rank_batch precondition check) that only need the
// ok bool. Extracted so the batch validator can avoid the
// three-blank-identifier dogsled pattern while still honouring
// dual-convention tolerance and the Options.Shadow flip.
func speciesExists(
	snapshot *pogopvp.Gamemaster, baseID string, opts CombatantOptions,
) bool {
	_, _, _, ok := resolveSpeciesLookup(snapshot, baseID, opts) //nolint:dogsled // intentional: only the bool is needed

	return ok
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

	// Pass the gamemaster snapshot so resolvedSpeciesIDForMoveset can
	// flip to the "_shadow" entry when Options.Shadow=true. Without
	// this, auto-resolve for shadow combatants returns the base
	// species' moveset instead of pvpoke's shadow-specific build.
	snapshot := tool.manager.Current()

	err = applyMovesetDefaults(ctx, tool.rankings, &params.Attacker, cpCap, params.Cup, snapshot, false, false)
	if err != nil {
		return fmt.Errorf("attacker moveset: %w", err)
	}

	err = applyMovesetDefaults(ctx, tool.rankings, &params.Defender, cpCap, params.Cup, snapshot, false, false)
	if err != nil {
		return fmt.Errorf("defender moveset: %w", err)
	}

	return nil
}

// buildEngineCombatant maps a tool-level Combatant (string-addressed
// moves, species id) to an engine-level pogopvp.Combatant with looked-up
// Species, Move structs, and a validated IV. Shadow-aware: when
// spec.Options.Shadow is true the gamemaster entry resolves through
// resolveSpeciesLookup and spec.resolvedSpeciesID /
// spec.shadowVariantMissing are populated for later echo to the
// response via resolvedFromSpec.
func buildEngineCombatant(
	snapshot *pogopvp.Gamemaster, spec *Combatant, shields int,
) (pogopvp.Combatant, error) {
	species, err := resolveSpeciesForSpec(snapshot, spec)
	if err != nil {
		return pogopvp.Combatant{}, err
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
		IsShadow:     spec.Options.Shadow || strings.HasSuffix(spec.resolvedSpeciesID, shadowSuffix),
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
