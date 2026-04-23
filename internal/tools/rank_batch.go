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
// species + league + CP cap + XL flag + Options as pvp_rank,
// applied to each IV triple in IVs. Batching saves N-1 round-trips
// when a client is sweeping IV space (typical use: "score my entire
// box of this species in one call"). Options applies batch-wide —
// every IV triple is evaluated against the same resolved species,
// so Options.Shadow=true sweeps the shadow variant's ranking. No
// cup parameter: pvp_rank returns rankings_by_cup on every result,
// so the batch reproduces that array per entry.
type RankBatchParams struct {
	Species string           `json:"species" jsonschema:"species id in the pvpoke gamemaster"`
	IVs     [][3]int         `json:"ivs" jsonschema:"list of [atk, def, sta] triples; each component 0..15"`
	League  string           `json:"league" jsonschema:"little|great|ultra|master"`
	CPCap   int              `json:"cp_cap,omitempty" jsonschema:"override (0 = league default); optimal level re-searched under the override"`
	XL      bool             `json:"xl,omitempty" jsonschema:"allow XL candy levels above 40"`
	Options CombatantOptions `json:"options,omitzero" jsonschema:"shadow / lucky / purified flags applied batch-wide"`
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
//
// RankingsByCup is hoisted to the top level because it is species-
// scoped (cup rankings do not depend on the caller's IVs); repeating
// it per-entry would balloon the payload 5-50× on typical box
// scoring workflows. Each entries[*].result still carries every
// other pvp_rank field, with its own RankingsByCup zeroed out.
type RankBatchResult struct {
	Species       string           `json:"species"`
	League        string           `json:"league"`
	CPCap         int              `json:"cp_cap"`
	RankingsByCup []CupRanking     `json:"rankings_by_cup,omitempty"`
	Entries       []RankBatchEntry `json:"entries"`
	SuccessCount  int              `json:"success_count"`
}

// RankBatchTool wraps the gamemaster + rankings managers. It reuses
// the single-IV RankTool handler internally so the response per
// entry is bit-for-bit identical to what pvp_rank would return
// standalone.
type RankBatchTool struct {
	manager *gamemaster.Manager
	rank    *RankTool
}

// NewRankBatchTool constructs the tool bound to the same managers
// the single-IV RankTool uses.
func NewRankBatchTool(manager *gamemaster.Manager, ranks *rankings.Manager) *RankBatchTool {
	return &RankBatchTool{
		manager: manager,
		rank:    NewRankTool(manager, ranks),
	}
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

// handle validates the batch-wide preconditions up front (gamemaster
// loaded, species known, league / cp_cap resolvable, IVs count in
// range) before the loop — batch-wide failures should fail the whole
// request with one error rather than producing N copies of the same
// error in the per-entry slice. The loop then invokes the single-IV
// RankTool handler for each IV; only per-IV errors (e.g. out-of-range
// IV components) surface as OK=false entries. Ctx.Err() is polled
// between entries so a client disconnect stops the sweep.
func (tool *RankBatchTool) handle(
	ctx context.Context,
	req *mcp.CallToolRequest,
	params RankBatchParams,
) (*mcp.CallToolResult, RankBatchResult, error) {
	resolvedCPCap, err := tool.validateBatch(ctx, &params)
	if err != nil {
		return nil, RankBatchResult{}, err
	}

	rankHandler := tool.rank.Handler()
	entries := make([]RankBatchEntry, 0, len(params.IVs))

	var (
		successCount  int
		rankingsByCup []CupRanking
	)

	for _, ivTriple := range params.IVs {
		if ctx.Err() != nil {
			return nil, RankBatchResult{}, fmt.Errorf("rank_batch cancelled: %w", ctx.Err())
		}

		entry := runRankBatchEntry(ctx, req, rankHandler, ivTriple, &params, resolvedCPCap)

		// Hoist the species-scoped rankings_by_cup once from the
		// first successful entry and strip it from every per-
		// entry result: the array is identical across IVs, so
		// repeating it per-entry multiplies payload size 5-50×
		// for no information gain.
		if entry.OK {
			if rankingsByCup == nil && len(entry.Result.RankingsByCup) > 0 {
				rankingsByCup = entry.Result.RankingsByCup
			}

			entry.Result.RankingsByCup = nil
			successCount++
		}

		entries = append(entries, entry)
	}

	return nil, RankBatchResult{
		Species:       params.Species,
		League:        params.League,
		CPCap:         resolvedCPCap,
		RankingsByCup: rankingsByCup,
		Entries:       entries,
		SuccessCount:  successCount,
	}, nil
}

// validateBatch runs every precondition that applies to the whole
// batch: context live, batch size in range, gamemaster loaded,
// species exists, league/cp_cap resolvable. Returns the resolved CP
// cap so the caller can echo it at the top level and pass it to each
// per-entry RankParams (bypassing the per-call resolveCPCap lookup
// inside RankTool.handle).
func (tool *RankBatchTool) validateBatch(
	ctx context.Context, params *RankBatchParams,
) (int, error) {
	err := ctx.Err()
	if err != nil {
		return 0, fmt.Errorf("rank_batch cancelled: %w", err)
	}

	if len(params.IVs) == 0 {
		return 0, ErrEmptyIVList
	}

	if len(params.IVs) > maxRankBatchSize {
		return 0, fmt.Errorf("%w: %d IVs exceeds %d",
			ErrTooManyIVs, len(params.IVs), maxRankBatchSize)
	}

	snapshot := tool.manager.Current()
	if snapshot == nil {
		return 0, ErrGamemasterNotLoaded
	}

	if !speciesExists(snapshot, params.Species, params.Options) {
		return 0, fmt.Errorf("%w: %q", ErrUnknownSpecies, params.Species)
	}

	resolvedCPCap, err := resolveCPCap(params.League, params.CPCap)
	if err != nil {
		return 0, err
	}

	return resolvedCPCap, nil
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
	resolvedCPCap int,
) RankBatchEntry {
	single := RankParams{
		Species: params.Species,
		IV:      ivTriple,
		League:  params.League,
		CPCap:   resolvedCPCap,
		XL:      params.XL,
		Options: params.Options,
	}

	_, result, err := handler(ctx, req, single)
	if err != nil {
		return RankBatchEntry{IV: ivTriple, OK: false, Error: err.Error()}
	}

	return RankBatchEntry{IV: ivTriple, OK: true, Result: result}
}
