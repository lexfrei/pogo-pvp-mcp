package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrUnknownCupRule is returned when the cup id is not in the
// current gamemaster's cups[] table.
var ErrUnknownCupRule = errors.New("unknown cup id")

// CupRulesParams is the JSON input for pvp_cup_rules. Empty Cup
// returns every cup's rule block; a named cup returns one entry.
type CupRulesParams struct {
	Cup string `json:"cup,omitempty" jsonschema:"cup id from the gamemaster (e.g. \"spring\"); empty = full table"`
}

// CupRuleFilter is the wire shape of one include / exclude rule.
type CupRuleFilter struct {
	FilterType string   `json:"filter_type"`
	Values     []string `json:"values"`
}

// CupRuleEntry is one (cup id → rule block) row. Include and
// Exclude lists preserve pvpoke's filter ordering (pvpoke applies
// Include first, then subtracts Exclude). PartySize / LevelCap are
// 0 when the cup does not override the league defaults.
type CupRuleEntry struct {
	Cup       string          `json:"cup"`
	Title     string          `json:"title"`
	Include   []CupRuleFilter `json:"include"`
	Exclude   []CupRuleFilter `json:"exclude"`
	PartySize int             `json:"party_size,omitempty"`
	LevelCap  int             `json:"level_cap,omitempty"`
}

// CupRulesResult is the JSON output. Entries is sorted by cup id
// so the output is deterministic regardless of gamemaster iteration
// order.
type CupRulesResult struct {
	Query   string         `json:"query,omitempty"`
	Entries []CupRuleEntry `json:"entries"`
}

// CupRulesTool is a read-only lookup over the gamemaster cups map.
type CupRulesTool struct {
	gm *gamemaster.Manager
}

// NewCupRulesTool constructs the tool bound to the gamemaster.
func NewCupRulesTool(gm *gamemaster.Manager) *CupRulesTool {
	return &CupRulesTool{gm: gm}
}

const cupRulesToolDescription = "Look up pvpoke cup rules from the current gamemaster. Each cup entry carries its " +
	"Include / Exclude filter lists (filter types: type / tag / id / evolution), plus optional PartySize and " +
	"LevelCap overrides. Pass cup=\"\" for every cup, or a specific id (e.g. \"spring\", \"all\", \"jungle\")."

// Tool returns the MCP tool registration.
func (*CupRulesTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_cup_rules",
		Description: cupRulesToolDescription,
	}
}

// Handler returns the MCP-typed handler function.
func (tool *CupRulesTool) Handler() mcp.ToolHandlerFor[CupRulesParams, CupRulesResult] {
	return tool.handle
}

// handle returns either one cup's rules or the full table.
func (tool *CupRulesTool) handle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	params CupRulesParams,
) (*mcp.CallToolResult, CupRulesResult, error) {
	err := ctx.Err()
	if err != nil {
		return nil, CupRulesResult{}, fmt.Errorf("cup_rules cancelled: %w", err)
	}

	snapshot := tool.gm.Current()
	if snapshot == nil {
		return nil, CupRulesResult{}, ErrGamemasterNotLoaded
	}

	if params.Cup == "" {
		return nil, buildAllCupRules(snapshot), nil
	}

	cup, ok := snapshot.Cups[params.Cup]
	if !ok {
		return nil, CupRulesResult{}, fmt.Errorf("%w: %q", ErrUnknownCupRule, params.Cup)
	}

	return nil, CupRulesResult{
		Query:   params.Cup,
		Entries: []CupRuleEntry{projectCupRule(&cup)},
	}, nil
}

// buildAllCupRules projects every cup in the snapshot, sorted by id
// for deterministic output.
func buildAllCupRules(snapshot *pogopvp.Gamemaster) CupRulesResult {
	entries := make([]CupRuleEntry, 0, len(snapshot.Cups))

	for id := range snapshot.Cups {
		cup := snapshot.Cups[id]
		entries = append(entries, projectCupRule(&cup))
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Cup < entries[j].Cup
	})

	return CupRulesResult{Entries: entries}
}

// projectCupRule lifts one engine Cup into the JSON wire shape.
func projectCupRule(cup *pogopvp.Cup) CupRuleEntry {
	return CupRuleEntry{
		Cup:       cup.ID,
		Title:     cup.Title,
		Include:   projectCupFilters(cup.Include),
		Exclude:   projectCupFilters(cup.Exclude),
		PartySize: cup.PartySize,
		LevelCap:  cup.LevelCap,
	}
}

// projectCupFilters clones the filter list so callers cannot mutate
// the snapshot's underlying slice through the response.
func projectCupFilters(filters []pogopvp.CupFilter) []CupRuleFilter {
	out := make([]CupRuleFilter, 0, len(filters))
	for i := range filters {
		out = append(out, CupRuleFilter{
			FilterType: filters[i].FilterType,
			Values:     append([]string(nil), filters[i].Values...),
		})
	}

	return out
}
