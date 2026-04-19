package tools

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrEmptyPokedexQuery is returned when PokedexLookupParams.Query is
// empty or whitespace-only. An empty query against a 1700-species
// gamemaster would otherwise return every row — a useless response
// that risks a large payload and masks a client-side input problem.
var ErrEmptyPokedexQuery = errors.New("query must not be empty")

// pokedexLookupResultLimit caps the number of matches returned so
// broad substring queries (e.g. "dra" matching ~20 species) stay
// within a reasonable response size. Callers needing the full list
// should narrow the query.
const pokedexLookupResultLimit = 10

// pokedexMatchesInitialCapacity is the starting capacity for the
// per-query result slice. 4 is a fair guess for a dex-number
// lookup (base + alolan + galarian + paldean fits) and for a
// substring scan.
const pokedexMatchesInitialCapacity = 4

// PokedexLookupParams is the JSON input for pvp_pokedex_lookup.
// Query accepts three shapes, dispatched in order:
//
//   - All-digit string → treated as a pokedex number; returns every
//     species with matching Dex (base forms plus regional /
//     alolan / galarian / etc. variants).
//   - Exact species id match → returns just that species.
//   - Otherwise → case-insensitive substring match against species
//     id and display name, up to pokedexLookupResultLimit entries.
//
// IncludeShadow controls whether shadow variants (species ids
// ending in "_shadow") appear in the result. Default false because
// Options.Shadow=true on battle tools is the canonical way to
// address shadow forms; the lookup tool surfaces the base species
// for the caller to feed back into those tools.
type PokedexLookupParams struct {
	Query         string `json:"query" jsonschema:"pokedex number, species id, or substring of name"`
	IncludeShadow bool   `json:"include_shadow,omitempty" jsonschema:"include _shadow variants in the result (default false)"`
}

// PokedexLookupMatch is one row in the response. Dex / Types / Tags
// echo the engine Species fields the caller is most likely to key
// off; Name mirrors the pvpoke-published display string.
type PokedexLookupMatch struct {
	SpeciesID string   `json:"species_id"`
	Name      string   `json:"name"`
	Dex       int      `json:"dex"`
	Types     []string `json:"types"`
	Tags      []string `json:"tags,omitempty"`
	Released  bool     `json:"released"`
}

// PokedexLookupResult is the JSON output. Matches is sorted by
// exact-match priority first (exact species id hit appears first
// when the query is also a valid id) then by pokedex number; the
// limit is pokedexLookupResultLimit (10 today).
type PokedexLookupResult struct {
	Query       string               `json:"query"`
	Matches     []PokedexLookupMatch `json:"matches"`
	Truncated   bool                 `json:"truncated,omitempty"`
	TotalBefore int                  `json:"total_before_limit,omitempty"`
}

// PokedexLookupTool wraps the gamemaster manager — no rankings
// dependency, no simulation. Pure indexed scan.
type PokedexLookupTool struct {
	gm *gamemaster.Manager
}

// NewPokedexLookupTool constructs the tool.
func NewPokedexLookupTool(gm *gamemaster.Manager) *PokedexLookupTool {
	return &PokedexLookupTool{gm: gm}
}

const pokedexLookupToolDescription = "Find species in the current gamemaster by pokedex number, species id, " +
	"or substring of name. Use when the client has a human-readable name (e.g. \"farigiraf\", \"galarian moltres\") " +
	"and needs the canonical species id to feed into pvp_rank / pvp_matchup / pvp_team_analysis. Shadow variants " +
	"excluded by default; set include_shadow=true to include them. Results capped at 10; narrow the query if more " +
	"than 10 species match."

// Tool returns the MCP registration.
func (*PokedexLookupTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_pokedex_lookup",
		Description: pokedexLookupToolDescription,
	}
}

// Handler returns the MCP-typed handler.
func (tool *PokedexLookupTool) Handler() mcp.ToolHandlerFor[PokedexLookupParams, PokedexLookupResult] {
	return tool.handle
}

// handle dispatches the query by shape and returns up to 10
// matches. An empty query is rejected up front rather than
// defaulting to "return everything".
func (tool *PokedexLookupTool) handle(
	_ context.Context,
	_ *mcp.CallToolRequest,
	params PokedexLookupParams,
) (*mcp.CallToolResult, PokedexLookupResult, error) {
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return nil, PokedexLookupResult{}, ErrEmptyPokedexQuery
	}

	snapshot := tool.gm.Current()
	if snapshot == nil {
		return nil, PokedexLookupResult{}, ErrGamemasterNotLoaded
	}

	matches := collectPokedexMatches(snapshot, query, params.IncludeShadow)

	total := len(matches)
	truncated := total > pokedexLookupResultLimit

	if truncated {
		matches = matches[:pokedexLookupResultLimit]
	}

	result := PokedexLookupResult{
		Query:   params.Query,
		Matches: matches,
	}

	if truncated {
		result.Truncated = true
		result.TotalBefore = total
	}

	return nil, result, nil
}

// collectPokedexMatches dispatches the query by shape (dex number,
// exact species id, substring) and returns the sorted result.
func collectPokedexMatches(
	snapshot *pogopvp.Gamemaster, query string, includeShadow bool,
) []PokedexLookupMatch {
	dexNum, err := strconv.Atoi(query)
	if err == nil && dexNum > 0 {
		return matchesByDex(snapshot, dexNum, includeShadow)
	}

	return matchesByNameOrID(snapshot, query, includeShadow)
}

// shouldSkipShadow reports whether a species id should be filtered
// out based on the IncludeShadow flag.
func shouldSkipShadow(speciesID string, includeShadow bool) bool {
	return !includeShadow && strings.HasSuffix(speciesID, shadowSuffix)
}

// matchesByDex returns every species whose Dex equals the given
// number. Order: base forms (no "_" in id) first, then variants
// alphabetically by id — gives the caller the canonical species
// first and then the alternative forms.
func matchesByDex(
	snapshot *pogopvp.Gamemaster, dexNum int, includeShadow bool,
) []PokedexLookupMatch {
	out := make([]PokedexLookupMatch, 0, pokedexMatchesInitialCapacity)

	for id := range snapshot.Pokemon {
		species := snapshot.Pokemon[id]
		if species.Dex != dexNum {
			continue
		}

		if shouldSkipShadow(species.ID, includeShadow) {
			continue
		}

		out = append(out, speciesToMatch(&species))
	}

	sort.Slice(out, func(lhs, rhs int) bool {
		lhsVariant := strings.Contains(out[lhs].SpeciesID, "_")
		rhsVariant := strings.Contains(out[rhs].SpeciesID, "_")

		if lhsVariant != rhsVariant {
			return !lhsVariant
		}

		return out[lhs].SpeciesID < out[rhs].SpeciesID
	})

	return out
}

// matchesByNameOrID returns the (exact id hit ∪ substring matches)
// set, sorted so the exact-id match comes first, then substring
// matches by dex number.
func matchesByNameOrID(
	snapshot *pogopvp.Gamemaster, query string, includeShadow bool,
) []PokedexLookupMatch {
	substringMatches := collectSubstringMatches(snapshot, query, includeShadow)

	exact, exactOK := snapshot.Pokemon[query]
	if !exactOK {
		return substringMatches
	}

	if shouldSkipShadow(exact.ID, includeShadow) {
		return substringMatches
	}

	// Drop any substring entry that duplicates the exact match so
	// the caller doesn't see the same species twice.
	filtered := make([]PokedexLookupMatch, 0, len(substringMatches))

	for i := range substringMatches {
		if substringMatches[i].SpeciesID == exact.ID {
			continue
		}

		filtered = append(filtered, substringMatches[i])
	}

	prefix := make([]PokedexLookupMatch, 0, 1+len(filtered))
	prefix = append(prefix, speciesToMatch(&exact))
	prefix = append(prefix, filtered...)

	return prefix
}

// collectSubstringMatches walks the gamemaster looking for a
// case-insensitive match on either the species id or the display
// name, excluding shadow variants unless include_shadow is set.
// Results are sorted by dex number then species id for stable
// output across runs.
func collectSubstringMatches(
	snapshot *pogopvp.Gamemaster, query string, includeShadow bool,
) []PokedexLookupMatch {
	lower := strings.ToLower(query)
	out := make([]PokedexLookupMatch, 0, pokedexMatchesInitialCapacity)

	for speciesID := range snapshot.Pokemon {
		species := snapshot.Pokemon[speciesID]
		if shouldSkipShadow(species.ID, includeShadow) {
			continue
		}

		if !strings.Contains(strings.ToLower(speciesID), lower) &&
			!strings.Contains(strings.ToLower(species.Name), lower) {
			continue
		}

		out = append(out, speciesToMatch(&species))
	}

	sort.Slice(out, func(lhs, rhs int) bool {
		if out[lhs].Dex != out[rhs].Dex {
			return out[lhs].Dex < out[rhs].Dex
		}

		return out[lhs].SpeciesID < out[rhs].SpeciesID
	})

	return out
}

// speciesToMatch projects engine Species to the wire shape. Types
// and Tags pass through unmodified (both already json-safe slices
// of strings).
func speciesToMatch(species *pogopvp.Species) PokedexLookupMatch {
	return PokedexLookupMatch{
		SpeciesID: species.ID,
		Name:      species.Name,
		Dex:       species.Dex,
		Types:     append([]string(nil), species.Types...),
		Tags:      append([]string(nil), species.Tags...),
		Released:  species.Released,
	}
}
