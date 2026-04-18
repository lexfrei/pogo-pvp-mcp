// Package cache implements a byte-capped least-recently-used (LRU) cache
// used by the MCP server to memoise rank/matchup/meta computations. The
// cache is goroutine-safe and sized in bytes rather than entries so it
// respects the process-level memory budget from --cache-size.
package cache

import (
	"container/list"
	"sync"
)

// Stats exposes the cache counters for debug endpoints.
type Stats struct {
	Entries   int
	BytesUsed int
	Hits      uint64
	Misses    uint64
}

// LRU is a thread-safe LRU cache indexed by string, bounded by the total
// size of stored byte slices. A zero capacity disables storage (every
// Set is a no-op) — callers use it to turn the cache off without
// branching at the call site.
type LRU struct {
	capacity  int
	mu        sync.Mutex
	items     map[string]*list.Element
	order     *list.List
	bytesUsed int
	hits      uint64
	misses    uint64
}

// entry is the payload stored inside each list element.
type entry struct {
	key   string
	value []byte
}

// NewLRU returns a cache with the given byte capacity. A capacity of
// zero disables the cache: Set discards input and Get always misses.
func NewLRU(capacityBytes int) *LRU {
	return &LRU{
		capacity: capacityBytes,
		items:    make(map[string]*list.Element),
		order:    list.New(),
	}
}

// Get returns the value stored under key and marks it as the most
// recently used entry. The second return value reports whether the key
// was present.
func (lru *LRU) Get(key string) ([]byte, bool) {
	lru.mu.Lock()
	defer lru.mu.Unlock()

	elem, ok := lru.items[key]
	if !ok {
		lru.misses++

		return nil, false
	}

	lru.order.MoveToFront(elem)
	lru.hits++

	return elementEntry(elem).value, true
}

// Set inserts or updates the value for key, evicting least-recently-used
// entries until the total stored size fits the capacity. Values larger
// than the capacity are silently dropped.
func (lru *LRU) Set(key string, value []byte) {
	size := len(value)

	lru.mu.Lock()
	defer lru.mu.Unlock()

	if lru.capacity <= 0 || size > lru.capacity {
		// Remove any previously stored entry under this key so Get does
		// not return a stale value.
		lru.removeLocked(key)

		return
	}

	if existing, ok := lru.items[key]; ok {
		lru.bytesUsed -= len(elementEntry(existing).value)
		lru.order.Remove(existing)
		delete(lru.items, key)
	}

	for lru.bytesUsed+size > lru.capacity {
		oldest := lru.order.Back()
		if oldest == nil {
			break
		}

		lru.evictLocked(oldest)
	}

	ent := &entry{key: key, value: value}
	elem := lru.order.PushFront(ent)
	lru.items[key] = elem
	lru.bytesUsed += size
}

// Stats returns a snapshot of the cache counters.
func (lru *LRU) Stats() Stats {
	lru.mu.Lock()
	defer lru.mu.Unlock()

	return Stats{
		Entries:   len(lru.items),
		BytesUsed: lru.bytesUsed,
		Hits:      lru.hits,
		Misses:    lru.misses,
	}
}

// removeLocked deletes the entry for key if present. Caller holds lru.mu.
func (lru *LRU) removeLocked(key string) {
	existing, ok := lru.items[key]
	if !ok {
		return
	}

	lru.bytesUsed -= len(elementEntry(existing).value)
	lru.order.Remove(existing)
	delete(lru.items, key)
}

// evictLocked removes the given list element from the index and order
// list. Caller holds lru.mu.
func (lru *LRU) evictLocked(elem *list.Element) {
	ent := elementEntry(elem)
	lru.bytesUsed -= len(ent.value)
	lru.order.Remove(elem)
	delete(lru.items, ent.key)
}

// elementEntry narrows a list element's opaque value to the *entry type
// we store. Every element inserted by LRU carries an *entry, so the
// assertion is safe; a failure indicates a programming error.
func elementEntry(elem *list.Element) *entry {
	ent, ok := elem.Value.(*entry)
	if !ok {
		panic("cache: list element does not hold *entry")
	}

	return ent
}
