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

// ErrUpstreamStatus wraps non-200 upstream responses.
var ErrUpstreamStatus = errors.New("rankings upstream returned non-200 status")

// fetchTimeout caps each rankings download.
const fetchTimeout = 30 * time.Second

// cacheDirPerm is the permission bits used for the local cache dir.
const cacheDirPerm = 0o750

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

// Manager owns the per-cap ranking snapshots. Get lazily fetches and
// caches on first access; subsequent calls return the in-memory
// snapshot. Thread-safe.
type Manager struct {
	baseURL  string
	localDir string
	client   *http.Client

	mu    sync.RWMutex
	cache map[int][]RankingEntry
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
		cache:    make(map[int][]RankingEntry),
	}, nil
}

// Get returns the rankings for the given CP cap. On first call the
// manager tries the local cache; on miss it fetches from upstream and
// persists to disk. Subsequent calls return the in-memory snapshot.
func (m *Manager) Get(ctx context.Context, cpCap int) ([]RankingEntry, error) {
	if !supportedCaps[cpCap] {
		return nil, fmt.Errorf("%w: %d not in {500, 1500, 2500, 10000}", ErrUnsupportedCap, cpCap)
	}

	m.mu.RLock()
	entries, ok := m.cache[cpCap]
	m.mu.RUnlock()

	if ok {
		return entries, nil
	}

	entries, err := m.loadLocal(cpCap)
	if err == nil {
		m.storeInMemory(cpCap, entries)

		return entries, nil
	}

	entries, err = m.fetchUpstream(ctx, cpCap)
	if err != nil {
		return nil, err
	}

	m.storeInMemory(cpCap, entries)

	return entries, nil
}

// storeInMemory caches the parsed entries under the write lock.
func (m *Manager) storeInMemory(cpCap int, entries []RankingEntry) {
	m.mu.Lock()
	m.cache[cpCap] = entries
	m.mu.Unlock()
}

// loadLocal reads the cached JSON for the given cap from disk.
func (m *Manager) loadLocal(cpCap int) ([]RankingEntry, error) {
	body, err := os.ReadFile(m.localPath(cpCap))
	if err != nil {
		return nil, fmt.Errorf("read local rankings: %w", err)
	}

	return parseEntries(body)
}

// fetchUpstream hits the pvpoke rankings URL for the given cap and
// persists the payload to disk. The parsed entries are returned.
func (m *Manager) fetchUpstream(ctx context.Context, cpCap int) ([]RankingEntry, error) {
	url := fmt.Sprintf("%s/all/overall/rankings-%s.json", m.baseURL, strconv.Itoa(cpCap))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

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

	err = m.persist(cpCap, body)
	if err != nil {
		return nil, err
	}

	return entries, nil
}

// localPath returns the on-disk cache location for the given CP cap.
func (m *Manager) localPath(cpCap int) string {
	return filepath.Join(m.localDir, fmt.Sprintf("rankings-%d.json", cpCap))
}

// persist writes the fetched payload to disk atomically via temp file
// + rename so partial writes cannot corrupt the cache.
func (m *Manager) persist(cpCap int, body []byte) error {
	err := os.MkdirAll(m.localDir, cacheDirPerm)
	if err != nil {
		return fmt.Errorf("mkdir %s: %w", m.localDir, err)
	}

	tmp, err := os.CreateTemp(m.localDir, fmt.Sprintf(".rankings-%d-*.tmp", cpCap))
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

	err = os.Rename(tmpName, m.localPath(cpCap))
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
