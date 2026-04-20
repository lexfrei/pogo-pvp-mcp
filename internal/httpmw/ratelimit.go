package httpmw

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// rateLimitJanitorInterval controls how often the background janitor
// scans the per-IP limiter map for idle entries to evict. Balanced
// between "eventually reclaim memory from stale IPs" and "don't burn
// CPU on an empty map" — a 5 minute sweep is more than fast enough
// given realistic public traffic patterns.
const rateLimitJanitorInterval = 5 * time.Minute

// rateLimitIdleEviction is how long a per-IP limiter sits unused
// before the janitor evicts it. Must be ≥ the burst-window a
// legitimate client could realistically pause for; 30 min covers
// the "LLM paused on a long answer" case.
const rateLimitIdleEviction = 30 * time.Minute

// rateLimitHardCap is a backstop on the size of the per-IP limiter
// map between janitor runs. An IPv6 /64 attacker could mint 2^64
// unique keys and burn memory before the 30-min eviction cycle
// fires; once we cross this threshold we evict the oldest entries
// synchronously on the next insert. Size chosen to cover a plausible
// public-endpoint load (tens of thousands of distinct callers per
// eviction window) without becoming a DoS surface itself.
const rateLimitHardCap = 10_000

// RateLimiter is a per-client-IP token-bucket limiter with TTL
// eviction. Construct with NewRateLimiter; shut down with Stop.
//
// Zero RPS disables the middleware entirely (every request passes
// through). This is the documented escape hatch for dev / tests.
// Non-zero RPS installs per-IP rate.Limiter entries on first use and
// lets a background janitor reap idle ones every 5 minutes.
type RateLimiter struct {
	rps   rate.Limit
	burst int

	mu   sync.Mutex
	data map[string]*rateLimiterEntry

	stopCh   chan struct{}
	stopOnce sync.Once
	stopped  chan struct{}
}

// rateLimiterEntry pairs a rate.Limiter with the last-seen timestamp
// the janitor uses to decide eviction. Keep as a struct (not just a
// Limiter) so the janitor doesn't need a parallel lastSeen map.
type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter constructs a per-IP limiter with the supplied
// RPS and burst budget. RPS ≤ 0 disables the middleware (every
// request passes through); burst is clamped to max(1, burst) when
// RPS > 0 so a caller passing (5, 0) still has a working limiter
// (burst=0 would reject every first request).
func NewRateLimiter(rps, burst int) *RateLimiter {
	limiter := &RateLimiter{
		rps:     rate.Limit(rps),
		burst:   burst,
		data:    make(map[string]*rateLimiterEntry),
		stopCh:  make(chan struct{}),
		stopped: make(chan struct{}),
	}

	if rps <= 0 {
		// Nothing to do — Stop is a no-op on the disabled path, but
		// we still close stopped so Stop() can be called symmetrically.
		close(limiter.stopped)

		return limiter
	}

	if limiter.burst < 1 {
		limiter.burst = 1
	}

	go limiter.janitor()

	return limiter
}

// Stop terminates the background janitor. Idempotent — safe to call
// multiple times. After Stop the limiter keeps responding to
// Middleware calls (using the snapshot of data at Stop time); idle
// entries just stop being evicted.
func (limiter *RateLimiter) Stop() {
	limiter.stopOnce.Do(func() {
		close(limiter.stopCh)
	})

	<-limiter.stopped
}

// Middleware returns the net/http middleware that consults the
// limiter on every request. On Allow→false, writes 429 with a
// text body and skips the downstream handler.
func (limiter *RateLimiter) Middleware(next http.Handler) http.Handler {
	if limiter.rps <= 0 {
		// RPS disabled — pass through without consulting the
		// limiter. Cheaper than the map lookup on the hot path.
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIP(r)

		rl := limiter.limiterFor(ip, time.Now())
		if !rl.Allow() {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)

			return
		}

		next.ServeHTTP(w, r)
	})
}

// janitor periodically evicts per-IP entries that have been idle
// beyond rateLimitIdleEviction. Runs until Stop.
func (limiter *RateLimiter) janitor() {
	defer close(limiter.stopped)

	ticker := time.NewTicker(rateLimitJanitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-limiter.stopCh:
			return
		case <-ticker.C:
			limiter.evictIdle(time.Now())
		}
	}
}

// evictIdle drops entries whose lastSeen is more than
// rateLimitIdleEviction before now. Called by the janitor goroutine
// and by unit tests via the public Middleware path.
func (limiter *RateLimiter) evictIdle(now time.Time) {
	cutoff := now.Add(-rateLimitIdleEviction)

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	for clientIP, entry := range limiter.data {
		if entry.lastSeen.Before(cutoff) {
			delete(limiter.data, clientIP)
		}
	}
}

// limiterFor returns (or creates) the rate.Limiter for the given
// client IP and updates its lastSeen timestamp. Centralises the
// map-locking so the middleware fast path is one call. Enforces
// rateLimitHardCap synchronously on insert so an attacker minting
// unique IPs faster than the janitor's 5-minute cycle cannot grow
// the map unboundedly.
func (limiter *RateLimiter) limiterFor(clientIP string, now time.Time) *rate.Limiter {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	entry, ok := limiter.data[clientIP]
	if !ok {
		if len(limiter.data) >= rateLimitHardCap {
			limiter.evictOldestLocked()
		}

		entry = &rateLimiterEntry{
			limiter: rate.NewLimiter(limiter.rps, limiter.burst),
		}
		limiter.data[clientIP] = entry
	}

	entry.lastSeen = now

	return entry.limiter
}

// evictOldestLocked removes the single oldest-seen entry from the
// per-IP map. Caller MUST hold limiter.mu. Only invoked when the
// map has reached rateLimitHardCap — the worst case drops a
// legitimate-but-idle limiter, but bounds memory even against an
// IPv6-range attacker. O(len(data)) but runs rarely (only on hard-
// cap saturation).
func (limiter *RateLimiter) evictOldestLocked() {
	var (
		oldestIP   string
		oldestSeen time.Time
		found      bool
	)

	for clientIP, entry := range limiter.data {
		if !found || entry.lastSeen.Before(oldestSeen) {
			oldestIP = clientIP
			oldestSeen = entry.lastSeen
			found = true
		}
	}

	if found {
		delete(limiter.data, oldestIP)
	}
}
