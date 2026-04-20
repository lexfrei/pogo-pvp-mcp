package httpmw_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/httpmw"
)

// TestRecover_PanicReturnsFiveHundred pins the core promise of the
// recover middleware: a panic in the downstream handler must not
// crash the process, the client must see a 500 response, and the
// next request to the same server must succeed (no wedged state).
func TestRecover_PanicReturnsFiveHundred(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	// The first call panics; the second call returns OK on the same
	// wrapped handler. A naive Recover that leaks into goroutine state
	// could fail the second call.
	callCount := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++

		if callCount == 1 {
			panic("synthetic handler failure")
		}

		w.WriteHeader(http.StatusOK)
	})

	handler := httpmw.Recover(logger)(inner)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	resp1 := mustGet(t, ts.URL)
	_ = resp1.Body.Close()

	if resp1.StatusCode != http.StatusInternalServerError {
		t.Errorf("first call status = %d, want %d", resp1.StatusCode, http.StatusInternalServerError)
	}

	// Logged the panic message so operators can triage.
	if !strings.Contains(buf.String(), "synthetic handler failure") {
		t.Errorf("logger output missing panic message; got:\n%s", buf.String())
	}

	resp2 := mustGet(t, ts.URL)
	_ = resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("second call status = %d, want 200 — recover leaked state", resp2.StatusCode)
	}
}

// TestRecover_NoPanicPassesThrough confirms the middleware is a no-op
// on the success path — the downstream handler's response reaches the
// client unchanged.
func TestRecover_NoPanicPassesThrough(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("brew"))
	})

	handler := httpmw.Recover(slog.Default())(inner)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	resp := mustGet(t, ts.URL)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d, want 418 (teapot preserved through middleware)", resp.StatusCode)
	}
}

// mustGet is a test-only GET helper that satisfies the noctx linter by
// threading t.Context() through NewRequestWithContext. Shared across
// the httpmw test files so each one doesn't reinvent the five-line
// request construction boilerplate.
func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	return resp
}
