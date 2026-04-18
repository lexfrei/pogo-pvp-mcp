package cache_test

import (
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/cache"
)

func TestLRU_GetAfterSetReturnsValue(t *testing.T) {
	t.Parallel()

	lru := cache.NewLRU(1024)

	lru.Set("k", []byte("value"))

	got, ok := lru.Get("k")
	if !ok {
		t.Fatal("Get returned not-ok after Set")
	}
	if string(got) != "value" {
		t.Errorf("Get = %q, want \"value\"", string(got))
	}
}

func TestLRU_GetMissReturnsNotOK(t *testing.T) {
	t.Parallel()

	lru := cache.NewLRU(1024)

	_, ok := lru.Get("absent")
	if ok {
		t.Error("Get on missing key returned ok")
	}
}

func TestLRU_EvictsOldestWhenCapacityExceeded(t *testing.T) {
	t.Parallel()

	// Each entry is 5 bytes of payload plus the key overhead.
	lru := cache.NewLRU(10)

	lru.Set("a", []byte("aaaaa"))
	lru.Set("b", []byte("bbbbb"))

	// Writing a third entry should evict "a" (oldest).
	lru.Set("c", []byte("ccccc"))

	if _, ok := lru.Get("a"); ok {
		t.Error("oldest entry \"a\" was not evicted")
	}
	if _, ok := lru.Get("b"); !ok {
		t.Error("entry \"b\" was unexpectedly evicted")
	}
	if _, ok := lru.Get("c"); !ok {
		t.Error("newest entry \"c\" missing")
	}
}

func TestLRU_GetRefreshesRecency(t *testing.T) {
	t.Parallel()

	lru := cache.NewLRU(10)

	lru.Set("a", []byte("aaaaa"))
	lru.Set("b", []byte("bbbbb"))

	// Touch "a" to make it most-recent.
	_, _ = lru.Get("a")

	// Writing a third entry should now evict "b" (oldest after touch).
	lru.Set("c", []byte("ccccc"))

	if _, ok := lru.Get("b"); ok {
		t.Error("entry \"b\" should have been evicted after refreshing \"a\"")
	}
	if _, ok := lru.Get("a"); !ok {
		t.Error("entry \"a\" missing after touch")
	}
}

func TestLRU_SetOverwriteUpdatesSize(t *testing.T) {
	t.Parallel()

	lru := cache.NewLRU(10)

	lru.Set("k", []byte("aaaaa"))
	lru.Set("k", []byte("bbbbbbbbbb")) // overwrite with larger value — triggers eviction of self

	got, ok := lru.Get("k")
	if !ok {
		t.Fatal("overwritten entry missing")
	}
	if string(got) != "bbbbbbbbbb" {
		t.Errorf("Get = %q, want \"bbbbbbbbbb\"", string(got))
	}
}

func TestLRU_RejectsValueLargerThanCapacity(t *testing.T) {
	t.Parallel()

	lru := cache.NewLRU(5)

	lru.Set("k", []byte("too_large_for_cache"))

	if _, ok := lru.Get("k"); ok {
		t.Error("value larger than capacity should not be stored")
	}
}

func TestLRU_OversizedOverwriteEvictsExisting(t *testing.T) {
	t.Parallel()

	// Set a small value, then write a too-large value under the same
	// key. The docstring promises "values larger than the capacity are
	// silently dropped"; this test pins the stronger guarantee that the
	// previously-stored value under that key is also gone, so Get does
	// not return a stale hit.
	lru := cache.NewLRU(5)

	lru.Set("k", []byte("aaa"))

	if _, ok := lru.Get("k"); !ok {
		t.Fatal("small value not stored")
	}

	lru.Set("k", []byte("way too large for the capacity"))

	if _, ok := lru.Get("k"); ok {
		t.Error("oversized overwrite left a stale entry under the same key")
	}
}

func TestLRU_ZeroCapacityDisables(t *testing.T) {
	t.Parallel()

	lru := cache.NewLRU(0)

	lru.Set("k", []byte("v"))

	if _, ok := lru.Get("k"); ok {
		t.Error("zero-capacity cache should not retain entries")
	}
}

func TestLRU_StatsReflectOperations(t *testing.T) {
	t.Parallel()

	lru := cache.NewLRU(100)

	lru.Set("a", []byte("aa"))
	lru.Set("b", []byte("bbb"))
	_, _ = lru.Get("a")
	_, _ = lru.Get("missing")

	stats := lru.Stats()
	if stats.Entries != 2 {
		t.Errorf("Stats.Entries = %d, want 2", stats.Entries)
	}
	if stats.Hits != 1 {
		t.Errorf("Stats.Hits = %d, want 1", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("Stats.Misses = %d, want 1", stats.Misses)
	}
	if stats.BytesUsed != 5 {
		t.Errorf("Stats.BytesUsed = %d, want 5", stats.BytesUsed)
	}
}
