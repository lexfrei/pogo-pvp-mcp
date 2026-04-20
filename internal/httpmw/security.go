package httpmw

import "net/http"

// hstsMaxAge is the Strict-Transport-Security max-age in seconds.
// One year matches the common industry default — long enough that
// repeat visitors get the pin, short enough to recover from a
// mistaken HTTPS rollout within a calendar window.
const hstsMaxAge = "31536000"

// hstsValue is the full HSTS header value. Pre-computed so every
// response shares the same string literal (cheap + consistent).
const hstsValue = "max-age=" + hstsMaxAge + "; includeSubDomains"

// SecurityHeaders returns a net/http middleware that stamps the
// baseline set of security-posture headers on every response:
//
//   - Strict-Transport-Security: max-age=1 year + includeSubDomains.
//     Pins HTTPS for repeat visitors. TLS itself terminates at the
//     proxy; HSTS is the browser-side reinforcement.
//   - X-Content-Type-Options: nosniff. Disables MIME sniffing;
//     critical when the server emits application/json but a
//     browser might speculate on executable content types.
//   - Referrer-Policy: no-referrer. MCP endpoints do not benefit
//     from Referer leakage; pinning no-referrer is the conservative
//     default.
//   - Content-Security-Policy: default-src 'none'. The MCP endpoint
//     does not return HTML; CSP deny-all makes any future regression
//     that accidentally serves script content fail closed.
//
// Headers are written BEFORE next.ServeHTTP so the downstream
// handler's own header writes (if any) don't clobber them — first
// write wins in net/http. Middleware is a no-op on the request
// flow; status code / body pass through unchanged.
func SecurityHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := w.Header()
			header.Set("Strict-Transport-Security", hstsValue)
			header.Set("X-Content-Type-Options", "nosniff")
			header.Set("Referrer-Policy", "no-referrer")
			header.Set("Content-Security-Policy", "default-src 'none'")

			next.ServeHTTP(w, r)
		})
	}
}
