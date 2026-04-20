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

// TestRealIP_TrustedChainReturnsLeftmostUntrusted pins the secure
// walk: when every proxy in the chain is in the trusted set, XFF
// walking right-to-left lands on the left-most untrusted entry —
// the original client IP. Chain: client -> proxy_b -> proxy_a ->
// our server; proxy_b and proxy_a both in trust list.
func TestRealIP_TrustedChainReturnsLeftmostUntrusted(t *testing.T) {
	t.Parallel()

	// 127.0.0.0/8 covers the httptest local peer (proxy_a closest
	// to us). 198.51.100.0/24 covers the hypothetical proxy_b
	// appearing earlier in the chain.
	trusted, err := httpmw.ParseTrustedProxies([]string{"127.0.0.0/8", "198.51.100.0/24"})
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

	// XFF convention: left-most = original client, each proxy
	// APPENDS to XFF. Here the chain recorded by proxy_a is
	// "client, proxy_b".
	req.Header.Set("X-Forwarded-For", spoofedClientIP+", 198.51.100.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if observed != spoofedClientIP {
		t.Errorf("ClientIP = %q, want %q (right-to-left walk must skip trusted proxy_b and return original client)",
			observed, spoofedClientIP)
	}
}

// TestRealIP_AttackerPrefixedXFFDoesNotSpoof pins the round-2
// review finding: real reverse proxies APPEND to XFF, so an
// attacker injecting "X-Forwarded-For: <victim-ip>" through a
// legitimate proxy ends up with "<victim-ip>, <attacker-real-ip>"
// on the server side. A naive left-most pick would return the
// attacker-chosen victim IP; right-to-left + skip-trusted returns
// the attacker's real IP instead. Rate limit applies to the real
// attacker, not the spoofed victim.
func TestRealIP_AttackerPrefixedXFFDoesNotSpoof(t *testing.T) {
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

	// Attacker injects the victim IP as the left-most entry. The
	// real proxy (at 127.0.0.1, trusted) would have appended the
	// attacker's TCP peer to the right — simulated here as
	// "<victim>, <attacker_tcp_peer>".
	const attackerTCPPeer = "10.0.0.99"

	req.Header.Set("X-Forwarded-For", spoofedClientIP+", "+attackerTCPPeer)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if observed == spoofedClientIP {
		t.Errorf("ClientIP = %q — attacker-prefixed XFF was accepted; rate limit would apply to the victim, not the attacker",
			observed)
	}

	if observed != attackerTCPPeer {
		t.Errorf("ClientIP = %q, want %q (right-most untrusted = attacker's real TCP peer)",
			observed, attackerTCPPeer)
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
