package httpmw

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// ErrInvalidTrustedProxy is returned by ParseTrustedProxies when a
// CIDR literal fails net.ParseCIDR. Wrapping a sentinel makes it
// matchable by callers via errors.Is without string comparison.
var ErrInvalidTrustedProxy = errors.New("invalid trusted proxy CIDR")

// xForwardedForHeader is Niantic-… er, the standard header name for
// the forwarded-client-ip chain from reverse proxies. Hoisted to a
// constant because it's referenced in the middleware and in godoc.
const xForwardedForHeader = "X-Forwarded-For"

// clientIPKey is the context-key type used to stash the resolved
// client IP on the request context. Unexported so external callers
// cannot collide; consumers read via ClientIP(r).
type clientIPKey struct{}

// TrustedProxySet is the parsed form of the operator-supplied trusted
// proxy CIDRs. Stored as a slice of *net.IPNet so Contains checks
// are O(len(trusted)) — fine for the handful of proxies a deployment
// typically has.
//
// A zero-value TrustedProxySet (nil slice) trusts NO proxy: every
// X-Forwarded-For is ignored and ClientIP falls back to RemoteAddr.
// This is the safe default when the operator has not configured
// the trust list.
type TrustedProxySet struct {
	nets []*net.IPNet
}

// ParseTrustedProxies validates and precomputes the trusted proxy
// CIDR list. Empty / nil input is valid and yields a set that trusts
// nobody. Any malformed entry returns an error wrapped around
// ErrInvalidTrustedProxy so config validation can fail loud at
// startup.
func ParseTrustedProxies(cidrs []string) (TrustedProxySet, error) {
	if len(cidrs) == 0 {
		return TrustedProxySet{}, nil
	}

	nets := make([]*net.IPNet, 0, len(cidrs))

	for _, raw := range cidrs {
		_, ipnet, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err != nil {
			return TrustedProxySet{}, fmt.Errorf("%w: %q: %w", ErrInvalidTrustedProxy, raw, err)
		}

		nets = append(nets, ipnet)
	}

	return TrustedProxySet{nets: nets}, nil
}

// contains reports whether ip is inside any of the trusted networks.
// An empty set always returns false (the default is "trust nobody").
func (s TrustedProxySet) contains(ip net.IP) bool {
	for _, ipnet := range s.nets {
		if ipnet.Contains(ip) {
			return true
		}
	}

	return false
}

// RealIP resolves the effective client IP and stashes it on the
// request context. Resolution rule:
//
//  1. Strip the port from r.RemoteAddr to get the peer IP.
//  2. If the peer IP is in the trusted-proxy set and r.Header
//     carries X-Forwarded-For, the first XFF entry is the client
//     IP (the left-most entry is the original client per
//     RFC 7239 / Forwarded-For convention; later entries are
//     proxy hops added on each traversal).
//  3. Otherwise the peer IP is the client IP — a direct client, or
//     an unauthenticated one trying to spoof the chain.
//
// Downstream handlers read the resolved value via ClientIP(r). Not
// writing to an HTTP header — the resolved IP is server-internal
// state used by rate limiting and structured logging.
func RealIP(trusted TrustedProxySet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientIP := resolveClientIP(r, trusted)
			ctx := context.WithValue(r.Context(), clientIPKey{}, clientIP)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// resolveClientIP implements the resolution rule from RealIP. Split
// out so the decision logic is unit-testable without spinning an
// HTTP server.
func resolveClientIP(r *http.Request, trusted TrustedProxySet) string {
	peerHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr without a port is pathological for HTTP — fall
		// back to the raw value rather than panicking.
		peerHost = r.RemoteAddr
	}

	peerIP := net.ParseIP(peerHost)
	if peerIP == nil || !trusted.contains(peerIP) {
		return peerHost
	}

	xff := r.Header.Get(xForwardedForHeader)
	if xff == "" {
		return peerHost
	}

	// Take the left-most entry — the original client. Trim whitespace
	// because the standard format is "ip1, ip2, ip3".
	first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
	if first == "" {
		return peerHost
	}

	return first
}

// ClientIP returns the effective client IP resolved by RealIP. If
// the middleware has not run (e.g. in a unit test that bypasses the
// chain), falls back to the host portion of r.RemoteAddr so rate-
// limit and logging code paths have a non-empty value.
func ClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey{}).(string); ok && ip != "" {
		return ip
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}
