package httpmw

import "net/http"

// MaxBytes wraps r.Body with http.MaxBytesReader so downstream
// reads fail with *http.MaxBytesError once n bytes have been
// consumed. Passing n = 0 disables the cap entirely (documented
// escape hatch for dev / tests that post arbitrary payloads).
//
// Does NOT write a 413 itself — the downstream handler's io.ReadAll
// or Decoder call is responsible for surfacing the error, and
// http.MaxBytesReader's injection into the ResponseWriter arranges
// for the status line to become 413 when the oversize condition is
// detected before the handler writes a response. This matches the
// behaviour documented in the stdlib (see the MaxBytesReader godoc).
func MaxBytes(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if n <= 0 {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}
