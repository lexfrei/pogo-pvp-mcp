package cli

import "context"

// runtimeKey is the unexported type used as the context key for the CLI
// [Runtime]. Wrapping it in a named type keeps other packages from
// colliding with the bare string.
type runtimeKey struct{}

// withRuntime returns a derived context that carries the Runtime.
func withRuntime(ctx context.Context, rt *Runtime) context.Context {
	return context.WithValue(ctx, runtimeKey{}, rt)
}

// runtimeFrom extracts the Runtime from a context set up by the root
// command. A missing runtime indicates a command was invoked outside
// the cobra PersistentPreRunE and is a programming error — the helper
// panics so the stack trace points at the call site.
func runtimeFrom(ctx context.Context) *Runtime {
	rt, ok := ctx.Value(runtimeKey{}).(*Runtime)
	if !ok {
		panic("cli: runtime not present in context — command invoked without root's PersistentPreRunE")
	}

	return rt
}
