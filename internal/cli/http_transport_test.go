package cli_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/cli"
	"github.com/lexfrei/pogo-pvp-mcp/internal/httpmw"
	"github.com/lexfrei/pogo-pvp-mcp/internal/mcpmw"
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

// TestMCPHTTPHandler_Phase2MiddlewareLogsEveryCall wires the real
// Phase 2 SDK middleware around buildWiredServer +
// NewMCPHTTPHandler and asserts that every MCP method reaching the
// server produces a structured log entry with the expected fields.
// This proves the middleware is attached to the same *mcp.Server
// used by NewMCPHTTPHandler — a regression that drops
// attachMCPMiddleware from serve.go would leave the log buffer
// empty and fail this test.
//
// The specific timeout-firing path is covered in mcpmw_test.go
// (TestLogging_FailureRecordsErrorLevelAndTimedOutFlag); here we
// only care about the integration wiring, not whether a tool
// actually exceeds its deadline (the test fixture's empty
// rankings make every call trivially fast).
func TestMCPHTTPHandler_Phase2MiddlewareLogsEveryCall(t *testing.T) {
	t.Parallel()

	mcpServer := buildWiredServer(t)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mcpServer.AddReceivingMiddleware(
		mcpmw.Logging(logger),
		// Disabled-tier budgets — we're not testing the timeout
		// firing here, only the log side of the wiring.
		mcpmw.Timeout(0, 0),
	)

	handler := cli.NewMCPHTTPHandler(mcpServer, nil)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "phase2-test", Version: "t"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer session.Close()

	_, err = session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "pvp_rank",
		Arguments: map[string]any{
			"species": integrationFixtureSpecies,
			"iv":      []int{0, 15, 15},
			"league":  "great",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	got := buf.String()

	// initialize call + tools/call pvp_rank — both must surface
	// through the Logging middleware attached to the same server
	// that NewMCPHTTPHandler dispatches to.
	wants := []string{
		"method=initialize",
		"tool=pvp_rank",
		"duration_ms=",
	}

	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("server log missing %q; full log:\n%s", want, got)
		}
	}
}

// TestMCPHTTPHandler_Phase3ChainBlocksOversizedBody wires the real
// Phase 3 middleware chain (Recover → RealIP → RateLimit → MaxBytes)
// around NewMCPHTTPHandler and proves that oversized request bodies
// are rejected by the middleware itself (413) rather than reaching
// the MCP handler. Guards against the round-2 review finding that
// MaxBytes alone doesn't guarantee 413 — the Content-Length
// short-circuit in the middleware must fire before any handler
// gets to choose a different status code.
func TestMCPHTTPHandler_Phase3ChainBlocksOversizedBody(t *testing.T) {
	t.Parallel()

	mcpServer := buildWiredServer(t)

	trusted, err := httpmw.ParseTrustedProxies(nil)
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}

	// Cap at 64 bytes; the MCP initialize payload is a few hundred,
	// so Content-Length always exceeds and every request 413s.
	limiter := httpmw.NewRateLimiter(0, 0)
	t.Cleanup(limiter.Stop)

	handler := httpmw.Chain(
		cli.NewMCPHTTPHandler(mcpServer, nil),
		httpmw.Recover(nil),
		httpmw.RealIP(trusted),
		limiter.Middleware,
		httpmw.MaxBytes(64),
	)

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	body := strings.Repeat("x", 4096)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413 (MaxBytes must short-circuit oversized Content-Length before the MCP SDK gets a chance to answer 400)",
			resp.StatusCode)
	}
}

// TestMCPHTTPHandler_Phase3ChainAllowsLegitimateRequest is the happy-
// path companion: the same chain with a generous 64 KiB cap must NOT
// block a normal initialize. Otherwise a zero-byte regression
// elsewhere would silently break production traffic.
func TestMCPHTTPHandler_Phase3ChainAllowsLegitimateRequest(t *testing.T) {
	t.Parallel()

	mcpServer := buildWiredServer(t)

	trusted, err := httpmw.ParseTrustedProxies(nil)
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}

	limiter := httpmw.NewRateLimiter(0, 0)
	t.Cleanup(limiter.Stop)

	handler := httpmw.Chain(
		cli.NewMCPHTTPHandler(mcpServer, nil),
		httpmw.Recover(nil),
		httpmw.RealIP(trusted),
		limiter.Middleware,
		httpmw.MaxBytes(65536),
	)

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "http-chain-test", Version: "t"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer session.Close()

	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	if len(listed.Tools) == 0 {
		t.Errorf("ListTools returned no tools through Phase 3 chain")
	}
}

// TestMCPHTTPHandler_ConcurrentWithInMemorySession proves the same
// *mcp.Server instance can serve an HTTP client and a second
// in-process client (the stdio analogue via NewInMemoryTransports)
// at the same time. Production wiring runs stdio and HTTP
// transports off one server construction — a regression where the
// SDK serialises session state globally would surface here as a
// deadlock or cross-session interference.
func TestMCPHTTPHandler_ConcurrentWithInMemorySession(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	mcpServer := buildWiredServer(t)

	// HTTP transport on its own httptest server.
	handler := cli.NewMCPHTTPHandler(mcpServer, nil)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	httpClient := mcp.NewClient(&mcp.Implementation{Name: "http", Version: "t"}, nil)
	httpSession, err := httpClient.Connect(ctx,
		&mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("httpClient.Connect: %v", err)
	}
	defer httpSession.Close()

	// In-memory transport on the SAME mcp.Server pointer.
	serverT, clientT := mcp.NewInMemoryTransports()

	_, err = mcpServer.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}

	memClient := mcp.NewClient(&mcp.Implementation{Name: "mem", Version: "t"}, nil)
	memSession, err := memClient.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("memClient.Connect: %v", err)
	}
	defer memSession.Close()

	// Issue ListTools on both sessions concurrently. Both must
	// succeed within a reasonable deadline; a server-wide mutex
	// would surface here as one blocking the other forever.
	errCh := make(chan error, 2)

	go func() {
		_, err := httpSession.ListTools(ctx, nil)
		errCh <- err
	}()

	go func() {
		_, err := memSession.ListTools(ctx, nil)
		errCh <- err
	}()

	for i := range 2 {
		err := <-errCh
		if err != nil {
			t.Errorf("concurrent ListTools[%d]: %v", i, err)
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
			"species": integrationFixtureSpecies,
			"iv":      []int{0, 15, 15},
			"league":  "great",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if result.IsError {
		t.Fatalf("CallTool returned IsError=true: %+v", result.Content)
	}

	// Parse the structured content to confirm the handler actually
	// returned a RankResult payload. A blackbox check on !IsError
	// alone would silently accept a no-op handler returning an empty
	// success.
	if result.StructuredContent == nil {
		t.Fatalf("StructuredContent = nil, want RankResult JSON payload")
	}

	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal StructuredContent: %v", err)
	}

	var payload struct {
		Species string  `json:"species"`
		CP      int     `json:"cp"`
		Level   float64 `json:"level"`
	}

	err = json.Unmarshal(raw, &payload)
	if err != nil {
		t.Fatalf("unmarshal RankResult: %v (raw=%s)", err, raw)
	}

	if payload.Species != integrationFixtureSpecies {
		t.Errorf("Species = %q, want %q (round-trip via HTTP must preserve input)",
			payload.Species, integrationFixtureSpecies)
	}

	if payload.CP <= 0 || payload.CP > 1500 {
		t.Errorf("CP = %d, want (0, 1500] for medicham under great league", payload.CP)
	}

	if payload.Level < 1 || payload.Level > 50 {
		t.Errorf("Level = %.1f, want within [1, 50]", payload.Level)
	}
}
