package httpmw_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/httpmw"
)

// TestSecurityHeaders_AllHeadersPresent pins the Phase 5 minimum
// security-header set: every response carries HSTS,
// X-Content-Type-Options, Referrer-Policy, and CSP. Drops would
// ease downgrade attacks, MIME-sniff injection, or referrer leak;
// the test locks the contract so a future refactor can't silently
// remove any single header.
func TestSecurityHeaders_AllHeadersPresent(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := httpmw.SecurityHeaders()(inner)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	resp := mustGet(t, ts.URL)
	defer resp.Body.Close()

	expected := map[string]string{
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "no-referrer",
		"Content-Security-Policy":   "default-src 'none'",
	}

	for name, wantValue := range expected {
		gotValue := resp.Header.Get(name)
		if gotValue != wantValue {
			t.Errorf("header %s = %q, want %q", name, gotValue, wantValue)
		}
	}
}

// TestSecurityHeaders_DownstreamHandlerStillRuns confirms the
// middleware is a no-op on the request flow — the inner handler's
// response body and status code reach the client unchanged.
func TestSecurityHeaders_DownstreamHandlerStillRuns(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("brew"))
	})

	handler := httpmw.SecurityHeaders()(inner)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	resp := mustGet(t, ts.URL)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d, want 418 (inner handler preserved)", resp.StatusCode)
	}
}
