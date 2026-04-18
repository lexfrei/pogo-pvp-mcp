package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrInvalidTopN is returned when the TopN field on MetaParams,
// TeamAnalysisParams, or TeamBuilderParams is negative.
var ErrInvalidTopN = errors.New("invalid top_n")

// defaultMetaTopN is the number of species returned when the caller
// leaves MetaParams.TopN at its zero value.
const defaultMetaTopN = 30

// MetaParams is the JSON input contract for pvp_meta.
type MetaParams struct {
	League string `json:"league" jsonschema:"great|ultra|master"`
	TopN   int    `json:"top_n,omitempty" jsonschema:"how many entries to return (default 30)"`
}

// MetaEntry mirrors one rankings row trimmed to the fields exposed to
// MCP clients. Rank is 1-based.
type MetaEntry struct {
	Rank        int      `json:"rank"`
	SpeciesID   string   `json:"species"`
	SpeciesName string   `json:"species_name"`
	Rating      int      `json:"rating"`
	Score       float64  `json:"score"`
	Moveset     []string `json:"moveset"`
	Product     int      `json:"product"`
	Atk         float64  `json:"atk"`
	Def         float64  `json:"def"`
	HP          int      `json:"hp"`
}

// MetaResult is the JSON output contract for pvp_meta.
type MetaResult struct {
	League  string      `json:"league"`
	CPCap   int         `json:"cp_cap"`
	Entries []MetaEntry `json:"entries"`
}

// MetaTool wraps a rankings.Manager and exposes the pvp_meta tool.
type MetaTool struct {
	manager *rankings.Manager
}

// NewMetaTool constructs a MetaTool bound to the given rankings manager.
func NewMetaTool(manager *rankings.Manager) *MetaTool {
	return &MetaTool{manager: manager}
}

// metaToolDescription is factored out so the struct-literal fits the
// line-length limit.
const metaToolDescription = "Return the top-N species in the pvpoke overall rankings for a PvP league: " +
	"rank, rating, recommended moveset, and display stats."

// Tool returns the MCP tool registration.
func (tool *MetaTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_meta",
		Description: metaToolDescription,
	}
}

// Handler returns the MCP-typed handler.
func (tool *MetaTool) Handler() mcp.ToolHandlerFor[MetaParams, MetaResult] {
	return tool.handle
}

// handle orchestrates the pvp_meta response. Validates League, TopN,
// fetches the rankings slice from the manager, and trims/annotates to
// MetaEntry.
func (tool *MetaTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params MetaParams,
) (*mcp.CallToolResult, MetaResult, error) {
	err := ctx.Err()
	if err != nil {
		return nil, MetaResult{}, fmt.Errorf("meta cancelled: %w", err)
	}

	if params.TopN < 0 {
		return nil, MetaResult{}, fmt.Errorf("%w: %d must be non-negative", ErrInvalidTopN, params.TopN)
	}

	cpCap, err := resolveCPCap(params.League, 0)
	if err != nil {
		return nil, MetaResult{}, err
	}

	entries, err := tool.manager.Get(ctx, cpCap)
	if err != nil {
		return nil, MetaResult{}, fmt.Errorf("rankings fetch: %w", err)
	}

	topN := params.TopN
	if topN == 0 {
		topN = defaultMetaTopN
	}

	topN = min(topN, len(entries))

	return nil, MetaResult{
		League:  params.League,
		CPCap:   cpCap,
		Entries: buildMetaEntries(entries[:topN]),
	}, nil
}

// buildMetaEntries projects rankings slice rows into MetaEntry with
// 1-based rank assignment.
func buildMetaEntries(entries []rankings.RankingEntry) []MetaEntry {
	out := make([]MetaEntry, len(entries))

	for i := range entries {
		entry := entries[i]
		out[i] = MetaEntry{
			Rank:        i + 1,
			SpeciesID:   entry.SpeciesID,
			SpeciesName: entry.SpeciesName,
			Rating:      entry.Rating,
			Score:       entry.Score,
			Moveset:     entry.Moveset,
			Product:     entry.Stats.Product,
			Atk:         entry.Stats.Atk,
			Def:         entry.Stats.Def,
			HP:          entry.Stats.HP,
		}
	}

	return out
}
