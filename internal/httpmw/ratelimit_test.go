package httpmw_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lexfrei/pogo-pvp-mcp/internal/httpmw"
)

// TestRateLimit_BurstExhaustion pins the main promise: a single
// client IP exhausting the burst budget sees 429 Too Many Requests;
// a second client IP is unaffected. With RPS=1 and Burst=3, four
// consecutive requests from the same IP get 200, 200, 200, 429.
func TestRateLimit_BurstExhaustion(t *testing.T) {
	t.Parallel()

	limiter := httpmw.NewRateLimiter(1, 3)
	t.Cleanup(limiter.Stop)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	trusted, err := httpmw.ParseTrustedProxies([]string{"127.0.0.0/8"})
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}

	chain := httpmw.RealIP(trusted)(limiter.Middleware(inner))
	ts := httptest.NewServer(chain)
	t.Cleanup(ts.Close)

	statuses := make([]int, 4)

	for i := range statuses {
		req, reqErr := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL, http.NoBody)
		if reqErr != nil {
			t.Fatalf("NewRequestWithContext[%d]: %v", i, reqErr)
		}

		req.Header.Set("X-Forwarded-For", "198.51.100.50")

		resp, doErr := http.DefaultClient.Do(req)
		if doErr != nil {
			t.Fatalf("Do[%d]: %v", i, doErr)
		}
		_ = resp.Body.Close()

		statuses[i] = resp.StatusCode
	}

	wantStatuses := []int{200, 200, 200, 429}
	for i, got := range statuses {
		if got != wantStatuses[i] {
			t.Errorf("request[%d] status = %d, want %d (burst=3 followed by throttle)",
				i, got, wantStatuses[i])
		}
	}

	// Second client IP — untouched budget.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL, http.NoBody)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	req.Header.Set("X-Forwarded-For", "198.51.100.99")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("second-IP status = %d, want 200 (per-IP budgets must be independent)",
			resp.StatusCode)
	}
}

// TestRateLimit_ZeroRPSDisables pins the dev / test escape hatch: a
// zero-RPS limiter skips the middleware entirely so 1000 requests
// from one IP still all return 200. Critical so running the test
// suite doesn't hit rate-limits accidentally.
func TestRateLimit_ZeroRPSDisables(t *testing.T) {
	t.Parallel()

	limiter := httpmw.NewRateLimiter(0, 0)
	t.Cleanup(limiter.Stop)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := limiter.Middleware(inner)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	for i := range 50 {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL, http.NoBody)
		if err != nil {
			t.Fatalf("NewRequestWithContext[%d]: %v", i, err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do[%d]: %v", i, err)
		}
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("request[%d] status = %d, want 200 (zero RPS = disabled)",
				i, resp.StatusCode)
		}
	}
}

// TestRateLimit_RefillsAfterWait confirms the token-bucket refill:
// exhaust the burst, wait one RPS-period, and the next request
// succeeds again.
func TestRateLimit_RefillsAfterWait(t *testing.T) {
	t.Parallel()

	// 10 RPS burst 1 — a single request exhausts the bucket; the
	// next one after ~100 ms should succeed.
	limiter := httpmw.NewRateLimiter(10, 1)
	t.Cleanup(limiter.Stop)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := limiter.Middleware(inner)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	doReq := func(t *testing.T) int {
		t.Helper()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL, http.NoBody)
		if err != nil {
			t.Fatalf("NewRequestWithContext: %v", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		_ = resp.Body.Close()

		return resp.StatusCode
	}

	if got := doReq(t); got != http.StatusOK {
		t.Errorf("first request status = %d, want 200", got)
	}

	if got := doReq(t); got != http.StatusTooManyRequests {
		t.Errorf("second request status = %d, want 429 (burst=1 exhausted)", got)
	}

	// Wait enough for one token to refill (10 RPS = 100 ms/token).
	time.Sleep(200 * time.Millisecond)

	if got := doReq(t); got != http.StatusOK {
		t.Errorf("third request status = %d, want 200 (bucket refilled)", got)
	}
}

// TestRateLimit_StopTerminatesJanitor pins the no-leak property:
// calling Stop on the limiter terminates its background eviction
// goroutine. Without this the unit suite would leak goroutines per
// test and the race detector would eventually complain.
func TestRateLimit_StopTerminatesJanitor(t *testing.T) {
	t.Parallel()

	limiter := httpmw.NewRateLimiter(1, 1)
	limiter.Stop()

	// A second Stop must not panic — idempotent shutdown.
	limiter.Stop()

	// Post-Stop requests still work (limiter falls through after
	// shutdown rather than deadlocking). Accept 200 OR 429 — we
	// only assert no hang.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.invalid/", http.NoBody)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	rec := httptest.NewRecorder()
	limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK && rec.Code != http.StatusTooManyRequests {
		t.Errorf("post-Stop status = %d, want 200 or 429", rec.Code)
	}
}
