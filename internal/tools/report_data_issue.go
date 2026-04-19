package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ReportDataIssueParams is intentionally empty — the tool is a
// zero-arg static response pointing callers at the GitHub issue
// workflow. Keeping the struct for future extensibility (a caller
// might want to pass a category hint one day) without shipping
// that surface prematurely.
type ReportDataIssueParams struct{}

// ReportDataIssueResult carries the guidance text. RepositoryURL
// and IssuesURL are callable links so an LLM client can surface
// them directly to the end user.
//
// ChecklistHints enumerates the structured information a good bug
// report should carry (tool name, exact input, observed output,
// expected output, authoritative source). The tool is deliberately
// NOT categorising the issue — any enum lock would rot as new
// classes of data drift appear, and the human issue triage is
// quick enough that structured categories add no value.
type ReportDataIssueResult struct {
	Message        string   `json:"message"`
	RepositoryURL  string   `json:"repository_url"`
	IssuesURL      string   `json:"issues_url"`
	ChecklistHints []string `json:"checklist_hints"`
}

// ReportDataIssueTool is a zero-dependency static-response tool.
// Its only job is to direct callers (human or LLM) to the GitHub
// issue tracker when they spot incorrect hardcoded data —
// weather-boost table, encounter-CP rules, powerup stardust
// buckets, second-move cost multipliers, etc. Several of the
// shipped tools carry hardcoded tables that can drift when
// Niantic adjusts mechanics; the issue path is the standard
// escalation.
type ReportDataIssueTool struct{}

// NewReportDataIssueTool constructs the tool. No dependencies
// because the response is fully static.
func NewReportDataIssueTool() *ReportDataIssueTool {
	return &ReportDataIssueTool{}
}

const reportDataIssueToolDescription = "Return guidance for reporting a data-accuracy issue in this MCP server. " +
	"Use when a caller spots an incorrect hardcoded value in a tool response (weather boost table, encounter " +
	"CP rules, powerup stardust buckets, second-move cost multipliers, or any other pinned fact). The response " +
	"carries the GitHub repository URL, the issues-tracker URL, and a checklist of information a good report " +
	"should include."

// Tool returns the MCP tool registration.
func (*ReportDataIssueTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_report_data_issue",
		Description: reportDataIssueToolDescription,
	}
}

// Handler returns the MCP-typed handler function.
func (tool *ReportDataIssueTool) Handler() mcp.ToolHandlerFor[ReportDataIssueParams, ReportDataIssueResult] {
	return tool.handle
}

// reportDataIssueRepositoryURL and reportDataIssueIssuesURL pin
// the canonical outbound links. Kept as package-private consts so
// they can be updated from one spot and asserted by the test
// suite; outside of a repo rename these are stable.
const (
	reportDataIssueRepositoryURL = "https://github.com/lexfrei/pogo-pvp-mcp"
	reportDataIssueIssuesURL     = "https://github.com/lexfrei/pogo-pvp-mcp/issues/new"
)

// reportDataIssueMessage explains the rationale to the caller
// (some data is hardcoded, drifts when Niantic adjusts mechanics,
// and community correction is the primary signal channel).
const reportDataIssueMessage = "Several tools in this MCP server carry hardcoded Niantic data (weather-boost " +
	"table, encounter CP rules, powerup stardust buckets, shadow/purified cost multipliers, evolution " +
	"structure). Those values can drift when Niantic adjusts a mechanic between the reference date stamped " +
	"in the source and the current live game. If you spot a mismatch between a tool response and the " +
	"authoritative source (Bulbapedia, in-game display, Niantic patch notes), open an issue on GitHub with " +
	"the checklist items below so the maintainer can verify and patch the table."

// handle returns the static response. No context work, no
// external I/O, no validation — this is a pure constant payload.
func (tool *ReportDataIssueTool) handle(
	_ context.Context,
	_ *mcp.CallToolRequest,
	_ ReportDataIssueParams,
) (*mcp.CallToolResult, ReportDataIssueResult, error) {
	return nil, ReportDataIssueResult{
		Message:       reportDataIssueMessage,
		RepositoryURL: reportDataIssueRepositoryURL,
		IssuesURL:     reportDataIssueIssuesURL,
		ChecklistHints: []string{
			"Name the tool that returned the incorrect value (e.g. pvp_second_move_cost).",
			"Quote the exact input parameters you passed, including any Options block.",
			"Quote the exact output the tool returned.",
			"State what you expected instead and why (cite Bulbapedia section, in-game screenshot, or Niantic patch note).",
			"Note the date you observed the mismatch — Niantic can revert or re-patch values without announcement.",
		},
	}, nil
}
