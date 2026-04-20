package httpmw_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/httpmw"
)

// spoofedClientIP is the fake client IP written into X-Forwarded-For
// by the RealIP-middleware tests. Hoisted to a const so the goconst
// linter doesn't complain about the two-references-is-a-constant
// threshold.
const spoofedClientIP = "203.0.113.45"

// TestRealIP_TrustedProxyHonoursXForwardedFor pins the happy path: a
// request from an IP in the trusted-proxy CIDR list surfaces the
// first entry of X-Forwarded-For as the effective client IP. Downstream
// handlers read the resolved IP via httpmw.ClientIP(r).
func TestRealIP_TrustedProxyHonoursXForwardedFor(t *testing.T) {
	t.Parallel()

	trusted, err := httpmw.ParseTrustedProxies([]string{"127.0.0.0/8"})
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}

	var observed string
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		observed = httpmw.ClientIP(r)
	})

	handler := httpmw.RealIP(trusted)(inner)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL, http.NoBody)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	req.Header.Set("X-Forwarded-For", "203.0.113.45, 198.51.100.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if observed != spoofedClientIP {
		t.Errorf("ClientIP = %q, want %q (first XFF entry honoured from trusted proxy)",
			observed, spoofedClientIP)
	}
}

// TestRealIP_UntrustedProxyIgnoresXForwardedFor pins the security
// invariant: an unauthenticated client that spoofs X-Forwarded-For
// must NOT be able to change its effective IP. With no trusted
// proxies configured, the header is always ignored; ClientIP returns
// RemoteAddr's host part.
func TestRealIP_UntrustedProxyIgnoresXForwardedFor(t *testing.T) {
	t.Parallel()

	// Empty trust list — no proxy is trusted.
	trusted, err := httpmw.ParseTrustedProxies(nil)
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}

	var observed string
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		observed = httpmw.ClientIP(r)
	})

	handler := httpmw.RealIP(trusted)(inner)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL, http.NoBody)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	req.Header.Set("X-Forwarded-For", spoofedClientIP)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	// RemoteAddr is 127.0.0.1:<ephemeral> from httptest; the middleware
	// strips the port. Anything spoofed should NOT leak through.
	if observed == spoofedClientIP {
		t.Errorf("ClientIP = %q; spoofed XFF from untrusted proxy leaked through", observed)
	}

	if observed != "127.0.0.1" {
		t.Errorf("ClientIP = %q, want \"127.0.0.1\" (RemoteAddr host)", observed)
	}
}

// TestParseTrustedProxies_InvalidCIDRRejected pins the config-load
// validation: a malformed CIDR string must return an error so the
// server fails loud at startup rather than silently accepting every
// X-Forwarded-For (by having no trust entries match).
func TestParseTrustedProxies_InvalidCIDRRejected(t *testing.T) {
	t.Parallel()

	_, err := httpmw.ParseTrustedProxies([]string{"not-a-cidr"})
	if err == nil {
		t.Errorf("ParseTrustedProxies(\"not-a-cidr\") = nil, want error")
	}
}

// TestClientIP_NoMiddlewareReturnsRemoteAddr pins the defensive
// behaviour of ClientIP when the RealIP middleware hasn't populated
// the context — e.g. a handler called outside the chain in tests.
// Returns the stripped-host form of r.RemoteAddr.
func TestClientIP_NoMiddlewareReturnsRemoteAddr(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.invalid/", http.NoBody)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	req.RemoteAddr = "198.51.100.7:54321"

	if got := httpmw.ClientIP(req); got != "198.51.100.7" {
		t.Errorf("ClientIP = %q, want %q (fallback to RemoteAddr host)", got, "198.51.100.7")
	}
}
