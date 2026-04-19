package rankings_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lexfrei/pogo-pvp-mcp/internal/rankings"
)

// testSpeciesMedicham keeps the id literal out of repeated assertions
// without hoisting a package-wide domain constant into the test binary.
const testSpeciesMedicham = "medicham"

// allOverall1500Path is the upstream URL tail the manager hits when no
// cup is supplied (cup resolves to "all"). Factored so both the server
// mux and the assertion read from one source of truth.
const allOverall1500Path = "/all/overall/rankings-1500.json"

// minimalRankings is a trimmed rankings payload carrying one entry with
// every field the parser cares about.
const minimalRankings = `[
  {
    "speciesId": "medicham",
    "speciesName": "Medicham",
    "rating": 700,
    "score": 95.2,
    "moveset": ["COUNTER", "ICE_PUNCH", "POWER_UP_PUNCH"],
    "matchups": [{"opponent": "azumarill", "rating": 650}, {"opponent": "sableye", "rating": 580}],
    "counters": [{"opponent": "trevenant", "rating": 320}],
    "stats": {"product": 2103, "atk": 106.9, "def": 139.4, "hp": 141}
  }
]`

func newTestRankingsServer(t *testing.T, payload string) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != allOverall1500Path {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)

	return server
}

func TestManager_GetFetchesAndCaches(t *testing.T) {
	t.Parallel()

	server := newTestRankingsServer(t, minimalRankings)

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	entries, err := mgr.Get(t.Context(), 1500, "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("entries count = %d, want 1", len(entries))
	}

	entry := entries[0]
	if entry.SpeciesID != testSpeciesMedicham {
		t.Errorf("SpeciesID = %q, want %s", entry.SpeciesID, testSpeciesMedicham)
	}
	if entry.Rating != 700 {
		t.Errorf("Rating = %d, want 700", entry.Rating)
	}
	if entry.Stats.Product != 2103 {
		t.Errorf("Stats.Product = %d, want 2103", entry.Stats.Product)
	}
	if len(entry.Moveset) != 3 {
		t.Errorf("Moveset len = %d, want 3", len(entry.Moveset))
	}
	if len(entry.Matchups) != 2 {
		t.Errorf("Matchups len = %d, want 2", len(entry.Matchups))
	}
}

func TestManager_GetReturnsCachedOnSecondCall(t *testing.T) {
	t.Parallel()

	hits := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalRankings))
	}))
	t.Cleanup(server.Close)

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Get(t.Context(), 1500, "")
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}

	_, err = mgr.Get(t.Context(), 1500, "")
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if hits != 1 {
		t.Errorf("server hits = %d, want 1 (second call should hit in-memory cache)", hits)
	}
}

func TestManager_GetUnknownCap(t *testing.T) {
	t.Parallel()

	server := newTestRankingsServer(t, minimalRankings)

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Get(t.Context(), 99999, "")
	if !errors.Is(err, rankings.ErrUnsupportedCap) {
		t.Errorf("error = %v, want wrapping ErrUnsupportedCap", err)
	}
}

func TestManager_PersistsAndReloads(t *testing.T) {
	t.Parallel()

	server := newTestRankingsServer(t, minimalRankings)
	dir := t.TempDir()

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: dir,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Get(t.Context(), 1500, "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Fresh manager pointing at an unreachable upstream — must read from disk.
	mgr2, err := rankings.NewManager(rankings.Config{
		BaseURL:  "http://example.invalid",
		LocalDir: dir,
	})
	if err != nil {
		t.Fatalf("NewManager (second): %v", err)
	}

	entries, err := mgr2.Get(t.Context(), 1500, "")
	if err != nil {
		t.Fatalf("Get from disk: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("entries count = %d, want 1 (from disk)", len(entries))
	}
}

func TestManager_InvalidConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  rankings.Config
	}{
		{"empty base url", rankings.Config{LocalDir: t.TempDir()}},
		{"empty local dir", rankings.Config{BaseURL: "http://example.com"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := rankings.NewManager(tc.cfg)
			if !errors.Is(err, rankings.ErrInvalidConfig) {
				t.Errorf("error = %v, want wrapping ErrInvalidConfig", err)
			}
		})
	}
}

// TestManager_GetSingleflight verifies that concurrent first-time
// calls coalesce into a single upstream fetch. Without the per-cap
// mutex guard a race between two Get(1500) could double-fetch.
func TestManager_GetSingleflight(t *testing.T) {
	t.Parallel()

	var (
		hits    atomic.Int64
		release = make(chan struct{})
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalRankings))
	}))
	t.Cleanup(server.Close)

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	const callers = 4

	var wg sync.WaitGroup

	wg.Add(callers)

	for range callers {
		go func() {
			defer wg.Done()

			_, _ = mgr.Get(t.Context(), 1500, "")
		}()
	}

	// Release after the test's goroutine scheduler has had a chance
	// to park every caller on the per-cap mutex / HTTP roundtrip.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := hits.Load(); got != 1 {
		t.Errorf("upstream hit count = %d, want 1 (singleflight)", got)
	}
}

// TestManager_StaleCacheTriggersRefetch pins the 24h TTL invariant:
// when the on-disk cache is older than cacheTTL, Get must go to the
// upstream for a fresh copy rather than serving the stale snapshot.
func TestManager_StaleCacheTriggersRefetch(t *testing.T) {
	t.Parallel()

	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalRankings))
	}))
	t.Cleanup(server.Close)

	dir := t.TempDir()

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: dir,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Get(t.Context(), 1500, "")
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}

	// Back-date the cache file past the 24h TTL and use a fresh
	// manager so the in-memory cache does not short-circuit. The
	// per-cup/role subdirectory is created by the manager on first
	// persist — Get implicitly targets the "overall" role.
	path := filepath.Join(dir, "all", "overall", "rankings-1500.json")
	old := time.Now().Add(-72 * time.Hour)

	err = os.Chtimes(path, old, old)
	if err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	mgr2, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: dir,
	})
	if err != nil {
		t.Fatalf("NewManager #2: %v", err)
	}

	_, err = mgr2.Get(t.Context(), 1500, "")
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if hits != 2 {
		t.Errorf("server hits = %d, want 2 (stale cache must re-fetch)", hits)
	}
}

// TestManager_GetWithCup verifies that a non-empty cup routes the
// upstream fetch to `/{cup}/overall/rankings-{cap}.json` and caches
// per (cup, cap) — two fetches with different cups do not collide.
func TestManager_GetWithCup(t *testing.T) {
	t.Parallel()

	const springPayload = `[{"speciesId":"venusaur","speciesName":"Venusaur","rating":900,` +
		`"score":98.0,"moveset":["VINE_WHIP","FRENZY_PLANT","SLUDGE_BOMB"],` +
		`"matchups":[],"counters":[],"stats":{"product":2800,"atk":120,"def":140,"hp":160}}]`

	var (
		allHits    atomic.Int64
		springHits atomic.Int64
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case allOverall1500Path:
			allHits.Add(1)

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(minimalRankings))
		case "/spring/overall/rankings-1500.json":
			springHits.Add(1)

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(springPayload))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	allEntries, err := mgr.Get(t.Context(), 1500, "")
	if err != nil {
		t.Fatalf("Get all: %v", err)
	}

	springEntries, err := mgr.Get(t.Context(), 1500, "spring")
	if err != nil {
		t.Fatalf("Get spring: %v", err)
	}

	if allEntries[0].SpeciesID != testSpeciesMedicham {
		t.Errorf("all[0] = %q, want %s", allEntries[0].SpeciesID, testSpeciesMedicham)
	}
	if springEntries[0].SpeciesID != "venusaur" {
		t.Errorf("spring[0] = %q, want venusaur", springEntries[0].SpeciesID)
	}

	if got := allHits.Load(); got != 1 {
		t.Errorf("all hits = %d, want 1", got)
	}
	if got := springHits.Load(); got != 1 {
		t.Errorf("spring hits = %d, want 1", got)
	}
}

// TestManager_GetCupCapNotFound verifies that an unsupported (cup, cap)
// pair surfaces as ErrUnknownCup. pvpoke publishes Spring rankings only
// at 1500 — asking for Spring at 500 must fail cleanly, not fall back
// to `all`.
func TestManager_GetCupCapNotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Get(t.Context(), 500, "spring")
	if !errors.Is(err, rankings.ErrUnknownCup) {
		t.Errorf("error = %v, want wrapping ErrUnknownCup", err)
	}
}

func TestManager_UpstreamError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	mgr, err := rankings.NewManager(rankings.Config{
		BaseURL:  server.URL,
		LocalDir: filepath.Join(t.TempDir(), "rankings"),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Get(t.Context(), 1500, "")
	if err == nil {
		t.Fatal("expected error for upstream 500")
	}
}
