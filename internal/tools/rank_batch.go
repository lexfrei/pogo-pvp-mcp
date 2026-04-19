package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrEmptyIVList is returned when RankBatchParams.IVs is nil or empty.
// Keeps the "caller explicitly supplied N things" contract the batch
// tool exists to serve — passing zero IVs is a pointless round-trip
// and almost always a client bug.
var ErrEmptyIVList = errors.New("empty iv list")

// ErrTooManyIVs is returned when RankBatchParams.IVs exceeds
// maxRankBatchSize, the DoS guard against an LLM-supplied enormous
// batch that would tie up the server producing a huge response.
var ErrTooManyIVs = errors.New("iv list exceeds maxRankBatchSize")

// maxRankBatchSize caps how many (IV) pvp_rank evaluations a single
// pvp_rank_batch call can request. Mirrors the MaxPoolSize discipline
// from team_builder / counter_finder — the batch tool itself is
// cheap per entry but a pathological request can still burn server
// time.
const maxRankBatchSize = 64

// RankBatchParams is the JSON input for pvp_rank_batch: the same
// species + league + cup + CP cap + XL flag as pvp_rank, applied to
// each IV triple in IVs. Batching saves N-1 round-trips when a
// client is sweeping IV space (typical use: "score my entire box of
// this species in one call").
type RankBatchParams struct {
	Species string   `json:"species" jsonschema:"species id in the pvpoke gamemaster (shadow variants use e.g. \"medicham_shadow\")"`
	IVs     [][3]int `json:"ivs" jsonschema:"list of [atk, def, sta] triples; each component 0..15"`
	League  string   `json:"league" jsonschema:"little|great|ultra|master"`
	Cup     string   `json:"cup,omitempty" jsonschema:"cup id from pvpoke; empty = open-league all"`
	CPCap   int      `json:"cp_cap,omitempty" jsonschema:"overrides the league default CP cap"`
	XL      bool     `json:"xl,omitempty" jsonschema:"allow XL candy levels above 40"`
}

// RankBatchEntry is one (IV → RankResult) pair in the batch response.
// Error carries the per-entry failure message when OK=false so the
// caller can tell which IV triple errored without losing the
// successful siblings.
type RankBatchEntry struct {
	IV     [3]int     `json:"iv"`
	OK     bool       `json:"ok"`
	Error  string     `json:"error,omitempty"`
	Result RankResult `json:"result,omitzero"`
}

// RankBatchResult is the JSON output: per-IV RankResult in the same
// order as the input IVs, plus a count of how many succeeded to
// spare the caller an iteration.
type RankBatchResult struct {
	Species      string           `json:"species"`
	League       string           `json:"league"`
	Cup          string           `json:"cup"`
	CPCap        int              `json:"cp_cap"`
	Entries      []RankBatchEntry `json:"entries"`
	SuccessCount int              `json:"success_count"`
}

// RankBatchTool wraps the gamemaster + rankings managers. It reuses
// the single-IV RankTool handler internally so the response per
// entry is bit-for-bit identical to what pvp_rank would return
// standalone.
type RankBatchTool struct {
	rank *RankTool
}

// NewRankBatchTool constructs the tool bound to the same managers
// the single-IV RankTool uses.
func NewRankBatchTool(manager *gamemaster.Manager, ranks *rankings.Manager) *RankBatchTool {
	return &RankBatchTool{rank: NewRankTool(manager, ranks)}
}

const rankBatchToolDescription = "Rank the same species + league under many IV triples in one call. Equivalent " +
	"to invoking pvp_rank per IV but saves the round-trip overhead for typical box-scoring workflows."

// Tool returns the MCP tool registration.
func (tool *RankBatchTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_rank_batch",
		Description: rankBatchToolDescription,
	}
}

// Handler returns the MCP-typed handler.
func (tool *RankBatchTool) Handler() mcp.ToolHandlerFor[RankBatchParams, RankBatchResult] {
	return tool.handle
}

// handle validates the batch preconditions, then loops over IVs
// invoking the single-IV RankTool handler for each. Per-entry errors
// are captured into the entry rather than bubbled up so a bad IV in
// position 5 does not kill the other 20 successful results. Checks
// ctx.Err() between entries so a client disconnect stops the sweep.
func (tool *RankBatchTool) handle(
	ctx context.Context,
	req *mcp.CallToolRequest,
	params RankBatchParams,
) (*mcp.CallToolResult, RankBatchResult, error) {
	err := ctx.Err()
	if err != nil {
		return nil, RankBatchResult{}, fmt.Errorf("rank_batch cancelled: %w", err)
	}

	if len(params.IVs) == 0 {
		return nil, RankBatchResult{}, ErrEmptyIVList
	}

	if len(params.IVs) > maxRankBatchSize {
		return nil, RankBatchResult{}, fmt.Errorf("%w: %d IVs exceeds %d",
			ErrTooManyIVs, len(params.IVs), maxRankBatchSize)
	}

	rankHandler := tool.rank.Handler()
	entries := make([]RankBatchEntry, 0, len(params.IVs))

	var successCount int

	for _, ivTriple := range params.IVs {
		if ctx.Err() != nil {
			return nil, RankBatchResult{}, fmt.Errorf("rank_batch cancelled: %w", ctx.Err())
		}

		entries = append(entries, runRankBatchEntry(ctx, req, rankHandler, ivTriple, &params))

		if entries[len(entries)-1].OK {
			successCount++
		}
	}

	return nil, RankBatchResult{
		Species:      params.Species,
		League:       params.League,
		Cup:          resolveCupLabel(params.Cup),
		CPCap:        params.CPCap,
		Entries:      entries,
		SuccessCount: successCount,
	}, nil
}

// runRankBatchEntry delegates to the single-IV RankTool handler with
// the batch's shared fields + the one per-entry IV triple, turning
// its error return into an OK=false entry. Keeps the batch handler
// under funlen.
func runRankBatchEntry(
	ctx context.Context,
	req *mcp.CallToolRequest,
	handler mcp.ToolHandlerFor[RankParams, RankResult],
	ivTriple [3]int,
	params *RankBatchParams,
) RankBatchEntry {
	single := RankParams{
		Species: params.Species,
		IV:      ivTriple,
		League:  params.League,
		Cup:     params.Cup,
		CPCap:   params.CPCap,
		XL:      params.XL,
	}

	_, result, err := handler(ctx, req, single)
	if err != nil {
		return RankBatchEntry{IV: ivTriple, OK: false, Error: err.Error()}
	}

	return RankBatchEntry{IV: ivTriple, OK: true, Result: result}
}
