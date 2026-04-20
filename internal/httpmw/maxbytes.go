package httpmw

import "net/http"

// MaxBytes caps the request body at n bytes using two layers:
//
//  1. **Content-Length short-circuit**: if the client sent a
//     Content-Length header larger than n, reply 413 immediately
//     without reading the body or invoking the downstream handler.
//     This is the common path for honest clients that declare
//     oversize up front.
//  2. **MaxBytesReader enforcement**: for chunked transfers (no
//     Content-Length) or deceptive declared sizes, wrap r.Body with
//     http.MaxBytesReader. Downstream reads fail with
//     *http.MaxBytesError once n bytes have been consumed. The
//     downstream handler's response code is handler-defined (the
//     MCP SDK writes 400 in this path); we do not override it —
//     clamping would require intercepting the ResponseWriter, and
//     clients hitting the streaming path are already buggy.
//
// Passing n = 0 disables the cap entirely (documented escape hatch
// for dev / tests that post arbitrary payloads).
func MaxBytes(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if n <= 0 {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > n {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)

				return
			}

			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}
