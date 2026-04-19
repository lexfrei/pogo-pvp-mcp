package cli_test

import (
	"net/http/httptest"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/cli"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPHTTPHandler_ListToolsOverHTTP pins the Phase 1 contract: a
// client connected via StreamableClientTransport to the server's
// NewMCPHTTPHandler wrapper sees every registered tool. The test
// drives the public transport the way a remote LLM would, proving
// that the same mcp.Server used by stdio also works unchanged under
// HTTP. httptest.NewServer handles listener lifecycle; no real port
// is bound.
//
// JSONResponse:true is set on the server options so httptest
// responses stay in single-response JSON rather than SSE streams —
// simpler for a blackbox test and matches the production config
// chosen in the plan (proxy-friendly, no SSE-hijack).
func TestMCPHTTPHandler_ListToolsOverHTTP(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	mcpServer := buildWiredServer(t)

	handler := cli.NewMCPHTTPHandler(mcpServer, nil)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "http-test-client", Version: "test"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL,
	}

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer session.Close()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	names := make(map[string]bool, len(listed.Tools))
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}

	// Aligned verbatim with integration_test.go's expected slice so a
	// dropped tool surfaces the same way on both the stdio and the
	// HTTP transports. Using len() >= 22 would silently allow a tool
	// rename (total count unchanged, but a specific name gone).
	expected := []string{
		"pvp_rank",
		"pvp_matchup",
		"pvp_cp_limits",
		"pvp_meta",
		"pvp_team_analysis",
		"pvp_team_builder",
		"pvp_species_info",
		"pvp_move_info",
		"pvp_type_matchup",
		"pvp_level_from_cp",
		"pvp_counter_finder",
		"pvp_evolution_preview",
		"pvp_rank_batch",
		"pvp_threat_coverage",
		"pvp_weather_boost",
		"pvp_encounter_cp_range",
		"pvp_cup_rules",
		"pvp_second_move_cost",
		"pvp_powerup_cost",
		"pvp_report_data_issue",
		"pvp_pokedex_lookup",
		"pvp_evolution_target",
	}

	for _, name := range expected {
		if !names[name] {
			t.Errorf("%s missing from HTTP-transport ListTools", name)
		}
	}
}

// TestMCPHTTPHandler_CallToolOverHTTP pins round-trip behaviour: the
// transport must accept a tool invocation and return the decoded
// result over HTTP. This covers the hot-path behaviour public LLM
// clients will exercise on every interaction.
func TestMCPHTTPHandler_CallToolOverHTTP(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	mcpServer := buildWiredServer(t)

	handler := cli.NewMCPHTTPHandler(mcpServer, nil)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "http-test-client", Version: "test"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL,
	}

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "pvp_rank",
		Arguments: map[string]any{
			"species": "medicham",
			"iv":      []int{0, 15, 15},
			"league":  "great",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if result.IsError {
		t.Errorf("CallTool returned IsError=true: %+v", result.Content)
	}
}
