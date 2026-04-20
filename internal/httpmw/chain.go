package httpmw

import "net/http"

// Chain composes any number of `func(http.Handler) http.Handler`
// middleware wrappers around handler. Wrappers are applied outer-to-
// inner in argument order, so Chain(h, A, B, C) produces a handler
// whose request flow is A → B → C → h.
//
// The variadic form matches the stdlib idioms for "middle-ware
// decorators" more closely than a slice parameter, and keeps call
// sites readable:
//
//	chained := httpmw.Chain(
//	    mcpHandler,
//	    httpmw.Recover(logger),
//	    httpmw.RealIP(trusted),
//	    limiter.Middleware,
//	    httpmw.MaxBytes(maxRequestBytes),
//	)
//
// With zero middlewares the handler is returned unchanged — no
// needless closure wrapping.
func Chain(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	// Apply from last to first so middlewares[0] ends up outermost.
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}

	return handler
}
