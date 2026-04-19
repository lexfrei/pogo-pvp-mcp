// Package rankings fetches and caches the pvpoke per-league ranking
// JSONs (rankings-500/1500/2500/10000.json) and exposes them as
// typed entries. These feed the meta / team_analysis / team_builder
// MCP tools — the engine-side ranker that eventually replaces them
// is Phase 5 work.
package rankings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// ErrInvalidConfig is returned by NewManager when the provided Config
// is missing a required field.
var ErrInvalidConfig = errors.New("invalid rankings config")

// ErrUnsupportedCap is returned by Get when the requested CP cap is
// not one of the canonical league values (500/1500/2500/10000).
var ErrUnsupportedCap = errors.New("unsupported cp cap")

// ErrUnknownCup is returned by Get when the upstream 404s on the
// requested (cup, cap) pair — either the cup does not exist in pvpoke
// or the cup exists but does not have rankings for that CP cap (e.g.
// Spring Cup is published only for 1500).
var ErrUnknownCup = errors.New("unknown cup or (cup, cap) combination")

// ErrUpstreamStatus wraps non-200 upstream responses other than 404.
var ErrUpstreamStatus = errors.New("rankings upstream returned non-200 status")

// defaultCup is the pvpoke URL segment for open-league (no cup) rankings.
const defaultCup = "all"

// Role identifies one of the per-role rankings pvpoke publishes
// alongside `overall`. These feed the pvp_meta role classifier in
// Phase F.2 and are served by the same URL pattern as `overall`
// with a different path segment.
type Role string

// The role identifiers published by pvpoke. RoleOverall is the
// default used by Get; the others are opt-in via GetRole.
const (
	RoleOverall  Role = "overall"
	RoleLeads    Role = "leads"
	RoleSwitches Role = "switches"
	RoleClosers  Role = "closers"
)

// fetchTimeout caps each rankings download.
const fetchTimeout = 30 * time.Second

// cacheDirPerm is the permission bits used for the local cache dir.
const cacheDirPerm = 0o750

// cacheTTL is how long a cached rankings file stays fresh before the
// manager re-fetches from upstream. 24h matches the gamemaster
// refresh interval so both datasets move together.
const cacheTTL = 24 * time.Hour

// supportedCaps enumerates the CP caps pvpoke publishes rankings for.
//
//nolint:gochecknoglobals // domain-constant lookup table
var supportedCaps = map[int]bool{
	500:   true,
	1500:  true,
	2500:  true,
	10000: true,
}

// Config configures the rankings manager.
type Config struct {
	// BaseURL is the pvpoke rankings root,
	// e.g. "https://raw.githubusercontent.com/pvpoke/pvpoke/master/src/data/rankings".
	BaseURL string

	// LocalDir is the directory under which per-cap cache files live.
	LocalDir string
}

// Matchup is one entry in RankingEntry.Matchups / .Counters.
type Matchup struct {
	Opponent string `json:"opponent"`
	Rating   int    `json:"rating"`
}

// Stats is the display stats pvpoke attaches to each entry.
type Stats struct {
	Product int     `json:"product"`
	Atk     float64 `json:"atk"`
	Def     float64 `json:"def"`
	HP      int     `json:"hp"`
}

// RankingEntry is one row of a rankings-*.json file.
type RankingEntry struct {
	SpeciesID   string    `json:"speciesId"`
	SpeciesName string    `json:"speciesName"`
	Rating      int       `json:"rating"`
	Score       float64   `json:"score"`
	Moveset     []string  `json:"moveset"`
	Matchups    []Matchup `json:"matchups"`
	Counters    []Matchup `json:"counters"`
	Stats       Stats     `json:"stats"`
}

// cacheKey identifies one (cup, cap, role) ranking snapshot.
// cup=="" is normalised to defaultCup on every entry into the cache
// so lookups and inserts always agree. role is always one of the
// Role constants (never empty).
type cacheKey struct {
	Cup  string
	Role Role
	Cap  int
}

// Manager owns the per-(cup, cap) ranking snapshots. Get lazily
// fetches and caches on first access; subsequent calls return the
// in-memory snapshot. Concurrent Get calls for the same (cup, cap)
// coalesce into a single fetch via per-key mutexes. Thread-safe.
type Manager struct {
	baseURL  string
	localDir string
	client   *http.Client

	mu       sync.RWMutex
	cache    map[cacheKey][]RankingEntry
	fetchMu  map[cacheKey]*sync.Mutex
	fetchMuG sync.Mutex
}

// NewManager validates the config and returns a ready Manager.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%w: BaseURL is empty", ErrInvalidConfig)
	}

	if cfg.LocalDir == "" {
		return nil, fmt.Errorf("%w: LocalDir is empty", ErrInvalidConfig)
	}

	return &Manager{
		baseURL:  cfg.BaseURL,
		localDir: cfg.LocalDir,
		client:   &http.Client{Timeout: fetchTimeout},
		cache:    make(map[cacheKey][]RankingEntry),
		fetchMu:  make(map[cacheKey]*sync.Mutex),
	}, nil
}

// Get returns the rankings for the given CP cap and cup. An empty cup
// resolves to the open-league "all" cup, matching pvpoke's URL scheme.
// On first call the manager tries the local cache; on miss it fetches
// from upstream and persists to disk. Subsequent calls return the
// in-memory snapshot. Concurrent first-time calls for the same
// (cup, cap) coalesce into a single fetch via per-key mutexes, so
// cold-start traffic from multiple tools does not trigger duplicate
// HTTP requests.
func (m *Manager) Get(ctx context.Context, cpCap int, cup string) ([]RankingEntry, error) {
	return m.GetRole(ctx, cpCap, cup, RoleOverall)
}

// GetRole behaves like Get but with an explicit pvpoke role segment:
// one of leads, switches, closers, consistency, attackers, chargers,
// or overall. Role rankings share the same URL shape as overall and
// are published per (cup, cap) in the same way. Callers routing the
// Role classifier use this; everyone else sticks with Get.
func (m *Manager) GetRole(
	ctx context.Context, cpCap int, cup string, role Role,
) ([]RankingEntry, error) {
	if !supportedCaps[cpCap] {
		return nil, fmt.Errorf("%w: %d not in {500, 1500, 2500, 10000}", ErrUnsupportedCap, cpCap)
	}

	if role == "" {
		role = RoleOverall
	}

	key := cacheKey{Cup: resolveCup(cup), Role: role, Cap: cpCap}

	entries, ok := m.lookup(key)
	if ok {
		return entries, nil
	}

	perKey := m.lockFor(key)
	perKey.Lock()
	defer perKey.Unlock()

	// Re-check under the per-key lock: a concurrent winner may have
	// populated the cache while we were waiting.
	entries, ok = m.lookup(key)
	if ok {
		return entries, nil
	}

	entries, err := m.loadLocal(key)
	if err == nil {
		m.storeInMemory(key, entries)

		return entries, nil
	}

	entries, err = m.fetchUpstream(ctx, key)
	if err != nil {
		return nil, err
	}

	m.storeInMemory(key, entries)

	return entries, nil
}

// resolveCup normalises empty strings to defaultCup so callers can
// pass "" for open-league rankings without special-casing.
func resolveCup(cup string) string {
	if cup == "" {
		return defaultCup
	}

	return cup
}

// lookup returns the cached entries under a shared read lock; the
// second return value reports whether the key was populated.
func (m *Manager) lookup(key cacheKey) ([]RankingEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entries, ok := m.cache[key]

	return entries, ok
}

// lockFor returns (creating if necessary) the per-key mutex used to
// serialize first-time fetches.
func (m *Manager) lockFor(key cacheKey) *sync.Mutex {
	m.fetchMuG.Lock()
	defer m.fetchMuG.Unlock()

	fetchMu, ok := m.fetchMu[key]
	if !ok {
		fetchMu = &sync.Mutex{}
		m.fetchMu[key] = fetchMu
	}

	return fetchMu
}

// storeInMemory caches the parsed entries under the write lock.
func (m *Manager) storeInMemory(key cacheKey, entries []RankingEntry) {
	m.mu.Lock()
	m.cache[key] = entries
	m.mu.Unlock()
}

// loadLocal reads the cached JSON for the given (cup, cap) from disk.
// The file is rejected if its mtime is older than [cacheTTL], so a
// stale cache forces a fresh upstream fetch instead of being served
// forever.
func (m *Manager) loadLocal(key cacheKey) ([]RankingEntry, error) {
	path := m.localPath(key)

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat local rankings: %w", err)
	}

	if time.Since(info.ModTime()) > cacheTTL {
		return nil, fmt.Errorf("%w: %s older than %v", errStaleCache, path, cacheTTL)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read local rankings: %w", err)
	}

	return parseEntries(body)
}

// errStaleCache is a private sentinel used by loadLocal so Get can
// distinguish "cache does not exist" from "cache is too old" and both
// paths fall through to fetchUpstream.
var errStaleCache = errors.New("rankings cache stale")

// fetchUpstream hits the pvpoke rankings URL for the given (cup, cap)
// and persists the payload to disk. The parsed entries are returned.
// A 404 response wraps ErrUnknownCup to let callers distinguish
// unsupported (cup, cap) pairs from generic upstream failures.
func (m *Manager) fetchUpstream(ctx context.Context, key cacheKey) ([]RankingEntry, error) {
	url := fmt.Sprintf("%s/%s/%s/rankings-%s.json",
		m.baseURL, key.Cup, key.Role, strconv.Itoa(key.Cap))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: cup=%q cap=%d (%s)", ErrUnknownCup, key.Cup, key.Cap, url)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s returned %d", ErrUpstreamStatus, url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	entries, err := parseEntries(body)
	if err != nil {
		return nil, err
	}

	err = m.persist(key, body)
	if err != nil {
		return nil, err
	}

	return entries, nil
}

// localDirFor returns the on-disk cache directory for the given
// (cup, role) pair.
func (m *Manager) localDirFor(cup string, role Role) string {
	return filepath.Join(m.localDir, cup, string(role))
}

// localPath returns the on-disk cache location for the given
// (cup, role, cap) triple.
func (m *Manager) localPath(key cacheKey) string {
	return filepath.Join(m.localDirFor(key.Cup, key.Role),
		fmt.Sprintf("rankings-%d.json", key.Cap))
}

// persist writes the fetched payload to disk atomically via temp file
// + rename so partial writes cannot corrupt the cache.
func (m *Manager) persist(key cacheKey, body []byte) error {
	dir := m.localDirFor(key.Cup, key.Role)

	err := os.MkdirAll(dir, cacheDirPerm)
	if err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, fmt.Sprintf(".rankings-%d-*.tmp", key.Cap))
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}

	tmpName := tmp.Name()

	_, writeErr := tmp.Write(body)
	closeErr := tmp.Close()

	if writeErr != nil {
		_ = os.Remove(tmpName)

		return fmt.Errorf("write temp: %w", writeErr)
	}

	if closeErr != nil {
		_ = os.Remove(tmpName)

		return fmt.Errorf("close temp: %w", closeErr)
	}

	err = os.Rename(tmpName, m.localPath(key))
	if err != nil {
		_ = os.Remove(tmpName)

		return fmt.Errorf("rename: %w", err)
	}

	return nil
}

// parseEntries decodes the JSON body into the typed slice.
func parseEntries(body []byte) ([]RankingEntry, error) {
	var entries []RankingEntry

	err := json.Unmarshal(body, &entries)
	if err != nil {
		return nil, fmt.Errorf("decode rankings json: %w", err)
	}

	return entries, nil
}
