package tools_test

import (
	"strings"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

// TestReportDataIssue_Payload pins the static-response contract:
// the tool must emit the GitHub repository URL, the issues tracker
// URL, a non-empty guidance message, and a non-empty checklist.
// If any of those drop out the user loses the escalation path.
func TestReportDataIssue_Payload(t *testing.T) {
	t.Parallel()

	tool := tools.NewReportDataIssueTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.ReportDataIssueParams{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Message == "" {
		t.Errorf("Message is empty")
	}

	// Repository is currently hosted at lexfrei/pvpoke-mcp (the Go
	// module path is already pogo-pvp-mcp but CLAUDE.md records
	// the repo rename as pending). Using the module path in the
	// URL returns 404. When the rename lands, flip both consts +
	// this test in one explicit commit; don't let the drift slip
	// through silently.
	if result.RepositoryURL != "https://github.com/lexfrei/pvpoke-mcp" {
		t.Errorf("RepositoryURL = %q, want live repo URL (rename pending per CLAUDE.md)",
			result.RepositoryURL)
	}

	if result.IssuesURL != "https://github.com/lexfrei/pvpoke-mcp/issues/new" {
		t.Errorf("IssuesURL = %q, want live new-issue URL (rename pending per CLAUDE.md)",
			result.IssuesURL)
	}

	if len(result.ChecklistHints) < 3 {
		t.Errorf("ChecklistHints has %d entries, want >= 3 (tool name / input / expected-vs-observed minimum)",
			len(result.ChecklistHints))
	}
}

// TestReportDataIssue_MessageCoversDriftRationale pins the
// rationale phrase in the message so a caller reading the
// response gets the why (Niantic mechanic drift) in-band. If a
// future edit strips the rationale, the test fails.
func TestReportDataIssue_MessageCoversDriftRationale(t *testing.T) {
	t.Parallel()

	tool := tools.NewReportDataIssueTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.ReportDataIssueParams{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	msgLower := strings.ToLower(result.Message)

	required := []string{"hardcoded", "niantic", "issue"}
	for _, phrase := range required {
		if !strings.Contains(msgLower, phrase) {
			t.Errorf("Message missing required phrase %q: %q", phrase, result.Message)
		}
	}
}

// TestReportDataIssue_DescriptionSanity locks the tool description
// text so LLM clients reading the schema can tell when this tool
// is the right one to reach for.
func TestReportDataIssue_DescriptionSanity(t *testing.T) {
	t.Parallel()

	tool := tools.NewReportDataIssueTool()
	desc := tool.Tool().Description

	if desc == "" {
		t.Fatal("description is empty")
	}

	descLower := strings.ToLower(desc)

	required := []string{"data", "issue", "github", "checklist"}
	for _, phrase := range required {
		if !strings.Contains(descLower, phrase) {
			t.Errorf("description missing fragment %q: %q", phrase, desc)
		}
	}
}
