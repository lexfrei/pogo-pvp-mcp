package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const integrationFixtureGamemaster = `{
  "id": "gamemaster",
  "timestamp": "2026-04-18 00:00:00",
  "pokemon": [
    {
      "dex": 308,
      "speciesId": "medicham",
      "speciesName": "Medicham",
      "baseStats": {"atk": 121, "def": 152, "hp": 155},
      "types": ["fighting", "psychic"],
      "fastMoves": ["COUNTER"],
      "chargedMoves": ["ICE_PUNCH"],
      "released": true
    }
  ],
  "moves": [
    {"moveId": "COUNTER", "name": "Counter", "type": "fighting", "power": 8, "energy": 0, "energyGain": 7, "cooldown": 1000, "turns": 2},
    {"moveId": "ICE_PUNCH", "name": "Ice Punch", "type": "ice", "power": 55, "energy": 40, "energyGain": 0, "cooldown": 500}
  ]
}`

// buildWiredServer stands up a fully-wired MCP server with all nine
// currently implemented tools registered and pre-populated managers.
func buildWiredServer(t *testing.T) *mcp.Server {
	t.Helper()

	gmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(integrationFixtureGamemaster))
	}))
	t.Cleanup(gmServer.Close)

	mgr, err := gamemaster.NewManager(config.GamemasterConfig{
		Source:    gmServer.URL,
		LocalPath: filepath.Join(t.TempDir(), "gm.json"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	err = mgr.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	rankingsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(rankingsServer.Close)

	ranks, err := rankings.NewManager(rankings.Config{
		BaseURL:  rankingsServer.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("NewManager rankings: %v", err)
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "pogo-pvp-mcp-test", Version: "test"}, nil)

	rankTool := tools.NewRankTool(mgr, ranks)
	mcp.AddTool(mcpServer, rankTool.Tool(), rankTool.Handler())

	matchupTool := tools.NewMatchupTool(mgr, ranks)
	mcp.AddTool(mcpServer, matchupTool.Tool(), matchupTool.Handler())

	cpLimitsTool := tools.NewCPLimitsTool(mgr)
	mcp.AddTool(mcpServer, cpLimitsTool.Tool(), cpLimitsTool.Handler())

	metaTool := tools.NewMetaTool(ranks)
	mcp.AddTool(mcpServer, metaTool.Tool(), metaTool.Handler())

	teamAnalysisTool := tools.NewTeamAnalysisTool(mgr, ranks)
	mcp.AddTool(mcpServer, teamAnalysisTool.Tool(), teamAnalysisTool.Handler())

	teamBuilderTool := tools.NewTeamBuilderTool(mgr, ranks)
	mcp.AddTool(mcpServer, teamBuilderTool.Tool(), teamBuilderTool.Handler())

	speciesInfoTool := tools.NewSpeciesInfoTool(mgr, ranks)
	mcp.AddTool(mcpServer, speciesInfoTool.Tool(), speciesInfoTool.Handler())

	moveInfoTool := tools.NewMoveInfoTool(mgr)
	mcp.AddTool(mcpServer, moveInfoTool.Tool(), moveInfoTool.Handler())

	typeMatchupTool := tools.NewTypeMatchupTool()
	mcp.AddTool(mcpServer, typeMatchupTool.Tool(), typeMatchupTool.Handler())

	return mcpServer
}

// TestIntegration_ListTools verifies that a client connected via the
// in-memory transport sees all nine currently implemented tools
// advertised. Guards against a silent drop of a registered tool from
// buildMCPServer.
func TestIntegration_ListTools(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	server := buildWiredServer(t)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	_, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)

	session, err := client.Connect(ctx, clientTransport, nil)
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
	}

	for _, name := range expected {
		if !names[name] {
			t.Errorf("%s missing from ListTools", name)
		}
	}
}

// TestIntegration_CallRank verifies the JSON-rpc round-trip for the
// pvp_rank tool: the client sends params, the server runs the handler
// against the live gamemaster, and the client can decode the result.
func TestIntegration_CallRank(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	server := buildWiredServer(t)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	_, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)

	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer session.Close()

	args := map[string]any{
		"species": "medicham",
		"iv":      []int{0, 15, 15},
		"league":  "great",
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "pvp_rank",
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if result.IsError {
		t.Fatalf("CallTool returned IsError: %+v", result)
	}

	rawJSON, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}

	var decoded tools.RankResult

	err = json.Unmarshal(rawJSON, &decoded)
	if err != nil {
		t.Fatalf("unmarshal RankResult: %v", err)
	}

	if decoded.Species != "medicham" {
		t.Errorf("Species = %q, want medicham", decoded.Species)
	}
	if decoded.CP <= 0 || decoded.CP > 1500 {
		t.Errorf("CP = %d, want in (0, 1500]", decoded.CP)
	}
	if decoded.StatProduct <= 0 {
		t.Errorf("StatProduct = %f, want positive", decoded.StatProduct)
	}
}
