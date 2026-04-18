// Package gamemaster holds the upstream pvpoke gamemaster payload in
// memory and refreshes it on demand. Consumers (MCP tools) read the
// currently-loaded Gamemaster via Manager.Current; the scheduler calls
// Manager.Refresh on a cadence driven by config.GamemasterConfig.
package gamemaster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
)

// ErrInvalidGamemasterConfig is returned by [NewManager] when the
// provided config is missing a required field.
var ErrInvalidGamemasterConfig = errors.New("invalid gamemaster config")

// ErrUpstreamStatus wraps non-200 upstream responses.
var ErrUpstreamStatus = errors.New("upstream returned non-200 status")

// fetchTimeout caps the upstream fetch so a hanging server does not
// stall the refresh goroutine forever.
const fetchTimeout = 30 * time.Second

// cacheDirPerm is the permission bits used for the parent directory of
// the local gamemaster cache file.
const cacheDirPerm = 0o750

// Manager owns the current Gamemaster snapshot plus the HTTP bits
// needed to refresh it from upstream. All public methods are safe to
// call concurrently.
type Manager struct {
	source    string
	localPath string
	client    *http.Client

	mu      sync.RWMutex
	current *pogopvp.Gamemaster
	etag    string
}

// NewManager validates the config and returns a ready-to-use Manager.
// The manager starts with no current gamemaster; callers invoke
// [Manager.Refresh] (hits upstream) or [Manager.LoadLocal] (reads the
// cached file) to populate it.
func NewManager(cfg config.GamemasterConfig) (*Manager, error) {
	if cfg.Source == "" {
		return nil, fmt.Errorf("%w: Source is empty", ErrInvalidGamemasterConfig)
	}

	if cfg.LocalPath == "" {
		return nil, fmt.Errorf("%w: LocalPath is empty", ErrInvalidGamemasterConfig)
	}

	return &Manager{
		source:    cfg.Source,
		localPath: cfg.LocalPath,
		client:    &http.Client{Timeout: fetchTimeout},
	}, nil
}

// Current returns the most recently parsed gamemaster or nil if no
// successful refresh / load has happened yet.
func (m *Manager) Current() *pogopvp.Gamemaster {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.current
}

// Refresh fetches the gamemaster from upstream, parses it, swaps the
// in-memory snapshot, and persists the raw bytes to the local cache
// path. When the upstream returns 304 Not Modified (matched against
// the previously-seen ETag) the existing snapshot is kept as-is.
func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.RLock()
	etag := m.etag
	m.mu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.source, http.NoBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", m.source, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s returned %d", ErrUpstreamStatus, m.source, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	return m.applyPayload(body, resp.Header.Get("ETag"))
}

// LoadLocal parses the cached file at LocalPath into the current
// snapshot. Useful on start-up to serve requests with the previous
// gamemaster copy while an async refresh runs. Note: the ETag used
// during the upstream fetch that produced the cache file is not
// recovered here, so the next [Refresh] after a cold start re-fetches
// the full body instead of issuing a conditional request.
func (m *Manager) LoadLocal() error {
	body, err := os.ReadFile(m.localPath)
	if err != nil {
		return fmt.Errorf("read local %s: %w", m.localPath, err)
	}

	return m.applyPayload(body, "")
}

// applyPayload parses the JSON, updates the current snapshot under the
// write lock, and writes the bytes through to disk so the next start-up
// can reuse them. The ETag is recorded for conditional fetches.
func (m *Manager) applyPayload(body []byte, etag string) error {
	parsed, err := pogopvp.ParseGamemaster(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("parse gamemaster: %w", err)
	}

	err = writeCache(m.localPath, body)
	if err != nil {
		return fmt.Errorf("cache write: %w", err)
	}

	m.mu.Lock()
	m.current = parsed

	if etag != "" {
		m.etag = etag
	}
	m.mu.Unlock()

	return nil
}

// writeCache writes the raw body to path, creating parent directories
// as needed. The temporary-then-rename dance keeps partial writes from
// corrupting the cache file.
func writeCache(path string, body []byte) error {
	dir := filepath.Dir(path)

	err := os.MkdirAll(dir, cacheDirPerm)
	if err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".gm-*.tmp")
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

	err = os.Rename(tmpName, path)
	if err != nil {
		_ = os.Remove(tmpName)

		return fmt.Errorf("rename to %s: %w", path, err)
	}

	return nil
}
