package scan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/erick303/spacestation/internal/config"
)

// sizeCache is a process-wide, persistent cache of directory sizes keyed by
// path + the directory's top-level mtime. When a tree's top-level mtime is
// unchanged we trust the cached size — for typical dev caches (node_modules
// updated by `npm install`, ~/Library/Caches/<app> updated by the app adding
// a new top-level entry) this is correct and lets warm scans skip walking
// huge trees entirely.
//
// Falsely-stale entries are bounded by the user: if they manually edit deep
// inside a cached dir without touching the parent dir's mtime, the cached
// size lags by however much they added. Acceptable for an estimate.
type sizeCache struct {
	mu      sync.RWMutex
	entries map[string]sizeCacheEntry
	dirty   bool
	path    string
}

type sizeCacheEntry struct {
	Mtime time.Time `json:"mtime"`
	Size  int64     `json:"size"`
	At    time.Time `json:"at"` // when this entry was computed
}

var (
	globalCache    *sizeCache
	globalCacheMu  sync.Mutex
	globalCachePath string
)

func loadGlobalCache() *sizeCache {
	globalCacheMu.Lock()
	defer globalCacheMu.Unlock()
	if globalCache != nil {
		return globalCache
	}
	p := globalCachePath
	if p == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			p = filepath.Join(home, ".config", "spacestation", "sizes.json")
		}
	}
	c := &sizeCache{
		entries: map[string]sizeCacheEntry{},
		path:    p,
	}
	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &c.entries)
	}
	globalCache = c
	return c
}

// SaveSizeCache flushes pending size-cache updates to disk. The TUI/CLI
// calls this once at end-of-scan.
func SaveSizeCache() error {
	c := loadGlobalCache()
	c.mu.RLock()
	dirty := c.dirty
	entries := c.entries
	path := c.path
	c.mu.RUnlock()
	if !dirty || path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// CachedDirSize returns the size of `root`, using a persistent cache keyed
// by (path, dir mtime). If the cache is stale or empty, DirSize is called
// and the result stored.
func CachedDirSize(root string, workers int) int64 {
	info, err := os.Stat(root)
	if err != nil {
		return 0
	}
	mtime := info.ModTime()

	c := loadGlobalCache()
	c.mu.RLock()
	e, ok := c.entries[root]
	c.mu.RUnlock()
	if ok && e.Mtime.Equal(mtime) {
		return e.Size
	}

	size := DirSize(root, workers)
	c.mu.Lock()
	c.entries[root] = sizeCacheEntry{Mtime: mtime, Size: size, At: time.Now()}
	c.dirty = true
	c.mu.Unlock()
	return size
}

// InvalidateSizeCache removes an entry. Call after a successful delete so a
// future scan re-computes.
func InvalidateSizeCache(root string) {
	c := loadGlobalCache()
	c.mu.Lock()
	if _, ok := c.entries[root]; ok {
		delete(c.entries, root)
		c.dirty = true
	}
	c.mu.Unlock()
}

// for tests
func resetGlobalCache(path string) {
	globalCacheMu.Lock()
	defer globalCacheMu.Unlock()
	globalCache = nil
	globalCachePath = path
	_ = config.Expand // keep import used; package needs config for path resolution elsewhere
}
