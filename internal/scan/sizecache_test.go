package scan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withTestCache redirects the package-level globalCache to a temp file and
// resets it on cleanup, so each test runs in isolation.
func withTestCache(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sizes.json")
	globalCacheMu.Lock()
	globalCache = nil
	globalCachePath = path
	globalCacheMu.Unlock()
	t.Cleanup(func() {
		globalCacheMu.Lock()
		globalCache = nil
		globalCachePath = ""
		globalCacheMu.Unlock()
	})
	return path
}

func TestSizeCacheRoundtrip(t *testing.T) {
	cachePath := withTestCache(t)

	// Make a tiny dir and prime the cache.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), make([]byte, 42), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CachedDirSize(context.Background(), dir, 2); got != 42 {
		t.Fatalf("first CachedDirSize = %d, want 42", got)
	}

	// SaveSizeCache should write to disk because the entry is fresh.
	if err := SaveSizeCache(); err != nil {
		t.Fatalf("SaveSizeCache: %v", err)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache file not written: %v", err)
	}

	// Drop the in-memory cache, then prove the on-disk file is read back.
	globalCacheMu.Lock()
	globalCache = nil
	globalCacheMu.Unlock()
	c := loadGlobalCache()
	c.mu.RLock()
	e, ok := c.entries[dir]
	c.mu.RUnlock()
	if !ok {
		t.Fatalf("expected dir in reloaded cache; got entries=%v", c.entries)
	}
	if e.Size != 42 {
		t.Errorf("reloaded size = %d, want 42", e.Size)
	}
}

func TestSaveSizeCacheNoopWhenClean(t *testing.T) {
	cachePath := withTestCache(t)

	// A fresh cache that was never written to has dirty=false. Save must not
	// create a file in that state — otherwise we'd churn the user's disk on
	// every JSON-mode run that returns no candidates.
	if err := SaveSizeCache(); err != nil {
		t.Fatalf("SaveSizeCache on clean cache: %v", err)
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Errorf("cache file written despite no dirty entries (err=%v)", err)
	}
}

func TestInvalidateSizeCache(t *testing.T) {
	withTestCache(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = CachedDirSize(context.Background(), dir, 2)

	c := loadGlobalCache()
	c.mu.RLock()
	_, ok := c.entries[dir]
	c.mu.RUnlock()
	if !ok {
		t.Fatalf("setup: entry not in cache")
	}

	InvalidateSizeCache(dir)
	c.mu.RLock()
	_, ok = c.entries[dir]
	c.mu.RUnlock()
	if ok {
		t.Errorf("InvalidateSizeCache left entry in place")
	}
}

func TestCachedDirSizeRecomputesOnMtimeChange(t *testing.T) {
	withTestCache(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CachedDirSize(context.Background(), dir, 2); got != 100 {
		t.Fatalf("setup: CachedDirSize = %d, want 100", got)
	}

	// Add a file and bump the directory's mtime explicitly so the second call
	// sees a fresh mtime and recomputes. (Adding a top-level entry already
	// touches the dir mtime, but make it deterministic across filesystems.)
	if err := os.WriteFile(filepath.Join(dir, "b"), make([]byte, 50), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(dir, future, future); err != nil {
		t.Fatal(err)
	}

	if got := CachedDirSize(context.Background(), dir, 2); got != 150 {
		t.Errorf("second CachedDirSize = %d, want 150 (recomputed after mtime change)", got)
	}
}

func TestSizeCacheLoadsCorruptFileAsEmpty(t *testing.T) {
	cachePath := withTestCache(t)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("not-json-at-all"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := loadGlobalCache()
	if len(c.entries) != 0 {
		t.Errorf("corrupt cache file should load as empty; got %d entries", len(c.entries))
	}
}

func TestSizeCacheOnDiskFormat(t *testing.T) {
	cachePath := withTestCache(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), make([]byte, 7), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = CachedDirSize(context.Background(), dir, 2)
	if err := SaveSizeCache(); err != nil {
		t.Fatalf("SaveSizeCache: %v", err)
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	// File is a JSON object keyed by path. We don't pin field shape (size
	// cache entries get new fields over time, e.g. Dev/Ino in H5), but the
	// path must be a key and the size must round-trip.
	var raw map[string]sizeCacheEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("on-disk format is not a path-keyed object: %v\n%s", err, string(data))
	}
	if raw[dir].Size != 7 {
		t.Errorf("on-disk size = %d, want 7", raw[dir].Size)
	}
}
