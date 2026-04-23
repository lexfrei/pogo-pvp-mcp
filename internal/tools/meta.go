package tools

import (
	"context"
	"errors"
	"fmt"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
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
	League string `json:"league" jsonschema:"little|great|ultra|master"`
	Cup    string `json:"cup,omitempty" jsonschema:"cup id from pvpoke (e.g. spring, retro, jungle); empty = open-league all"`
	TopN   int    `json:"top_n,omitempty" jsonschema:"how many entries to return (default 30)"`
}

// MetaEntry mirrors one rankings row trimmed to the fields exposed to
// MCP clients. Rank is 1-based. Role is the best-fit classification
// (lead / switch / closer / flex) based on where the species appears
// in pvpoke's per-role rankings; omitted when the role fetch failed
// or the species is not present in any of them. Moveset carries
// per-move Legacy / Elite flags so clients can tell pvpoke-recommended
// permanently-removed moves (legacy) and Elite TM / Community Day
// moves (elite) apart from regular learnables.
type MetaEntry struct {
	Rank        int       `json:"rank"`
	SpeciesID   string    `json:"species"`
	SpeciesName string    `json:"species_name"`
	Rating      int       `json:"rating"`
	Score       float64   `json:"score"`
	Moveset     []MoveRef `json:"moveset"`
	Product     int       `json:"product"`
	Atk         float64   `json:"atk"`
	Def         float64   `json:"def"`
	HP          int       `json:"hp"`
	Role        string    `json:"role,omitempty"`
}

// MetaResult is the JSON output contract for pvp_meta. Cup echoes
// the resolved cup so clients see whether a missing/empty input
// defaulted to the open-league `all` bucket.
type MetaResult struct {
	League  string      `json:"league"`
	Cup     string      `json:"cup"`
	CPCap   int         `json:"cp_cap"`
	Entries []MetaEntry `json:"entries"`
}

// MetaTool wraps a rankings.Manager plus a gamemaster.Manager — the
// former drives the top-N and role selection, the latter resolves
// per-move Legacy / Elite flags against each species' LegacyMoves /
// EliteMoves. gm may be nil in tests that don't care about
// restricted-move tagging; in that case every Moveset entry gets
// Legacy=false and Elite=false.
type MetaTool struct {
	manager *rankings.Manager
	gm      *gamemaster.Manager
}

// NewMetaTool constructs a MetaTool. ranks is required; gm may be
// nil — Legacy / Elite flags then fall through to false.
func NewMetaTool(manager *rankings.Manager, gm *gamemaster.Manager) *MetaTool {
	return &MetaTool{manager: manager, gm: gm}
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

	entries, err := tool.manager.Get(ctx, cpCap, params.Cup)
	if err != nil {
		return nil, MetaResult{}, fmt.Errorf("rankings fetch: %w", err)
	}

	topN := params.TopN
	if topN == 0 {
		topN = defaultMetaTopN
	}

	topN = min(topN, len(entries))

	metaEntries := buildMetaEntries(tool.snapshotOrNil(), entries[:topN])
	assignRoles(ctx, tool.manager, cpCap, params.Cup, metaEntries)

	return nil, MetaResult{
		League:  params.League,
		Cup:     resolveCupLabel(params.Cup),
		CPCap:   cpCap,
		Entries: metaEntries,
	}, nil
}

// roleRankGapThreshold is the number of positions by which the best
// per-role rank must beat the second-best for the tool to assign a
// concrete role. Below the threshold, the species is flagged "flex".
// Tuned at 5 on plan guidance; revisit after collecting real data.
const roleRankGapThreshold = 5

// assignRoles populates MetaEntry.Role for every top-N entry by
// comparing each species' position across the three per-role
// rankings (leads / switches / closers). Best-effort: a fetch error
// for any role leaves the entries with an empty role (omitted).
func assignRoles(
	ctx context.Context, manager *rankings.Manager, cpCap int, cup string,
	entries []MetaEntry,
) {
	leads := indexRole(ctx, manager, cpCap, cup, rankings.RoleLeads)
	switches := indexRole(ctx, manager, cpCap, cup, rankings.RoleSwitches)
	closers := indexRole(ctx, manager, cpCap, cup, rankings.RoleClosers)

	for i := range entries {
		entries[i].Role = classifyRole(entries[i].SpeciesID, leads, switches, closers)
	}
}

// indexRole fetches one per-role rankings slice and returns it as a
// speciesID → rank (1-based) map. On fetch failure returns nil so
// the classifier falls through to the missing-position path.
func indexRole(
	ctx context.Context, manager *rankings.Manager, cpCap int, cup string, role rankings.Role,
) map[string]int {
	entries, err := manager.GetRole(ctx, cpCap, cup, role)
	if err != nil {
		return nil
	}

	index := make(map[string]int, len(entries))
	for i := range entries {
		index[entries[i].SpeciesID] = i + 1
	}

	return index
}

// missingRank is the sentinel rank assigned to a species absent from
// a role's ranking slice — large enough that any real rank wins,
// small enough to not overflow in arithmetic.
const missingRank = 1 << 20

// classifyRole picks the best-fitting role for a species. The
// species' rank in each of the three per-role rankings is inspected;
// the smallest rank wins if it beats every other role's rank by at
// least roleRankGapThreshold positions, otherwise the species gets
// the "flex" label. Absent from every role map → empty string so
// the output omits the field.
func classifyRole(species string, leads, switches, closers map[string]int) string {
	candidates := [3]struct {
		name string
		rank int
	}{
		{"lead", rankFromIndex(leads, species)},
		{"switch", rankFromIndex(switches, species)},
		{"closer", rankFromIndex(closers, species)},
	}

	if candidates[0].rank == missingRank &&
		candidates[1].rank == missingRank &&
		candidates[2].rank == missingRank {
		return ""
	}

	best, runnerUp := 0, 1
	if candidates[runnerUp].rank < candidates[best].rank {
		best, runnerUp = runnerUp, best
	}

	for i := 2; i < len(candidates); i++ {
		if candidates[i].rank < candidates[best].rank {
			runnerUp, best = best, i
		} else if candidates[i].rank < candidates[runnerUp].rank {
			runnerUp = i
		}
	}

	if candidates[runnerUp].rank-candidates[best].rank >= roleRankGapThreshold {
		return candidates[best].name
	}

	return "flex"
}

// rankFromIndex returns the species' position in the index (1-based)
// or missingRank when absent.
func rankFromIndex(index map[string]int, species string) int {
	if rank, ok := index[species]; ok {
		return rank
	}

	return missingRank
}

// resolveCupLabel mirrors rankings.resolveCup at the tool boundary:
// both an empty input and the explicit "all" spelling echo as "all"
// in results so clients see what was actually applied. Clients can
// pass either form on input — the single label on output stays
// consistent ("all" for the open-league slice). Kept here (not
// re-exported from the rankings package) to avoid widening that API
// surface for one string.
func resolveCupLabel(cup string) string {
	if cup == "" || cup == openLeagueCupID {
		return openLeagueCupID
	}

	return cup
}

// snapshotOrNil returns the gamemaster snapshot for legacy tagging,
// or nil when the tool was constructed without a gamemaster manager
// (test mode). Callers treat nil as "every move has Legacy=false".
func (tool *MetaTool) snapshotOrNil() *pogopvp.Gamemaster {
	if tool.gm == nil {
		return nil
	}

	return tool.gm.Current()
}

// buildMetaEntries projects rankings slice rows into MetaEntry with
// 1-based rank assignment. When snapshot is non-nil, Moveset entries
// are tagged with per-species Legacy / Elite status via moveRefsFrom;
// otherwise both flags fall through to false.
func buildMetaEntries(
	snapshot *pogopvp.Gamemaster, entries []rankings.RankingEntry,
) []MetaEntry {
	out := make([]MetaEntry, len(entries))

	for i := range entries {
		entry := entries[i]

		var species *pogopvp.Species

		if snapshot != nil {
			if s, ok := snapshot.Pokemon[entry.SpeciesID]; ok {
				species = &s
			}
		}

		out[i] = MetaEntry{
			Rank:        i + 1,
			SpeciesID:   entry.SpeciesID,
			SpeciesName: entry.SpeciesName,
			Rating:      entry.Rating,
			Score:       entry.Score,
			Moveset:     moveRefsFrom(species, entry.Moveset),
			Product:     entry.Stats.Product,
			Atk:         entry.Stats.Atk,
			Def:         entry.Stats.Def,
			HP:          entry.Stats.HP,
		}
	}

	return out
}
