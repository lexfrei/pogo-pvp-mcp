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

	if result.RepositoryURL != "https://github.com/lexfrei/pogo-pvp-mcp" {
		t.Errorf("RepositoryURL = %q, want canonical repo URL", result.RepositoryURL)
	}

	if result.IssuesURL != "https://github.com/lexfrei/pogo-pvp-mcp/issues/new" {
		t.Errorf("IssuesURL = %q, want canonical new-issue URL", result.IssuesURL)
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
