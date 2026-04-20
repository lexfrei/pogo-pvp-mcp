package httpmw_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/httpmw"
)

// TestChain_OrdersMiddlewareOuterToInner pins the composition order
// of httpmw.Chain: the first argument wraps the outermost, the last
// wraps just above the final handler. Proven via a shared
// order-capturing slice — each fake middleware appends its name on
// entry. The expected order (recover → securityHeaders → realIP →
// rateLimit → maxBytes → handler) is exactly what the production
// Chain call site uses.
func TestChain_OrdersMiddlewareOuterToInner(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		order []string
	)

	record := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				order = append(order, name)
				mu.Unlock()

				next.ServeHTTP(w, r)
			})
		}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		order = append(order, "handler")
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	})

	handler := httpmw.Chain(
		inner,
		record("recover"),
		record("securityHeaders"),
		record("realIP"),
		record("rateLimit"),
		record("maxBytes"),
	)

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL, http.NoBody)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	want := []string{"recover", "securityHeaders", "realIP", "rateLimit", "maxBytes", "handler"}

	mu.Lock()
	defer mu.Unlock()

	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v (length mismatch)", order, want)
	}

	for i, name := range order {
		if name != want[i] {
			t.Errorf("order[%d] = %q, want %q (full order: got %v, want %v)",
				i, name, want[i], order, want)
		}
	}
}

// TestChain_EmptyMiddlewaresReturnsHandler pins the no-op path: with
// no middleware args, Chain returns the handler unchanged. Guards
// against future refactors that silently wrap the zero case in a
// needless closure.
func TestChain_EmptyMiddlewaresReturnsHandler(t *testing.T) {
	t.Parallel()

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true

		w.WriteHeader(http.StatusNoContent)
	})

	chained := httpmw.Chain(inner)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.invalid/", http.NoBody)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	rec := httptest.NewRecorder()
	chained.ServeHTTP(rec, req)

	if !called {
		t.Errorf("inner handler not invoked")
	}

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}
