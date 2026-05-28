package scan

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/erick303/spacestation/internal/config"
)

// buildTree builds a synthetic project tree.
//   /root
//     /projA
//       /src                       (regular file)
//       /node_modules              (artifact, 100 bytes)
//         /pkg/index.js
//         /pkg/big.bin             (sentinel — must NOT be classified)
//     /projB
//       /.venv                     (artifact)
//         /lib/file.py
//       /target                    (artifact, classified Rust)
//         /debug/exec
//     /projC
//       /.git                      (skipped)
//       /src/index.ts
func buildTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mkfile := func(rel string, content string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkfile("projA/src/index.ts", "console.log('hi')")
	mkfile("projA/node_modules/pkg/index.js", strings.Repeat("a", 50))
	mkfile("projA/node_modules/pkg/big.bin", strings.Repeat("b", 50))
	mkfile("projB/.venv/lib/file.py", strings.Repeat("c", 30))
	mkfile("projB/target/debug/exec", strings.Repeat("d", 70))
	mkfile("projC/.git/HEAD", "ref: refs/heads/main")
	mkfile("projC/src/index.ts", "x")
	return root
}

func TestWalkClassifiesAndPrunes(t *testing.T) {
	root := buildTree(t)

	cfg := config.Default()
	cfg.Scan.ProjectRoots = []string{root}
	cfg.Scan.IncludeFixedPaths = false
	cfg.Scan.IncludeDownloads = false
	cfg.Scan.IncludeTrash = false
	cfg.Scan.IncludeScreenshots = false

	cands := Run(context.Background(), Options{Cfg: cfg, Workers: 4}, nil)

	if len(cands) != 3 {
		t.Fatalf("expected 3 candidates (node_modules, .venv, target); got %d: %+v", len(cands), candsSummary(cands))
	}

	byCat := map[Category]Candidate{}
	for _, c := range cands {
		byCat[c.Category] = c
	}

	if c, ok := byCat[CatNodeModules]; !ok {
		t.Errorf("missing node_modules candidate")
	} else {
		if !strings.HasSuffix(c.Path, "projA/node_modules") {
			t.Errorf("unexpected node_modules path: %s", c.Path)
		}
		if c.SizeBytes != 100 {
			t.Errorf("expected node_modules size 100, got %d", c.SizeBytes)
		}
	}
	if c, ok := byCat[CatPython]; !ok {
		t.Errorf("missing .venv candidate")
	} else if c.SizeBytes != 30 {
		t.Errorf("expected .venv size 30, got %d", c.SizeBytes)
	}
	if c, ok := byCat[CatRust]; !ok {
		t.Errorf("missing target candidate")
	} else if c.SizeBytes != 70 {
		t.Errorf("expected target size 70, got %d", c.SizeBytes)
	}

	// Ensure we did NOT recurse into node_modules and pick up nested matches.
	for _, c := range cands {
		if strings.Contains(c.Path, "node_modules/") {
			t.Errorf("scanner descended into node_modules: %s", c.Path)
		}
	}
}

func TestWalkSkipsDotGit(t *testing.T) {
	root := buildTree(t)
	// Put a node_modules INSIDE .git — must not be picked up.
	deep := filepath.Join(root, "projC/.git/node_modules/x")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "y"), []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Scan.ProjectRoots = []string{root}
	cfg.Scan.IncludeFixedPaths = false
	cfg.Scan.IncludeDownloads = false
	cfg.Scan.IncludeTrash = false
	cfg.Scan.IncludeScreenshots = false

	cands := Run(context.Background(), Options{Cfg: cfg, Workers: 4}, nil)
	for _, c := range cands {
		if strings.Contains(c.Path, "/.git/") {
			t.Errorf("scanner entered .git: %s", c.Path)
		}
	}
}

func TestDirSize(t *testing.T) {
	dir := t.TempDir()
	for i, sz := range []int{10, 25, 100} {
		path := filepath.Join(dir, "f"+strconv.Itoa(i))
		if err := os.WriteFile(path, make([]byte, sz), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := DirSize(context.Background(), dir, 2)
	want := int64(135)
	if got != want {
		t.Errorf("DirSize = %d, want %d", got, want)
	}
}

func TestDirSizeDeduplicatesHardlinks(t *testing.T) {
	dir := t.TempDir()

	// One real 1 KB file, then 3 hardlinks pointing at the same inode.
	src := filepath.Join(dir, "original")
	content := make([]byte, 1024)
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		link := filepath.Join(dir, "link"+strconv.Itoa(i))
		if err := os.Link(src, link); err != nil {
			t.Fatal(err)
		}
	}

	// Without dedup: 4 names × 1024 bytes = 4096. With dedup: 1024.
	got := DirSize(context.Background(), dir, 2)
	want := int64(1024)
	if got != want {
		t.Errorf("DirSize with 4 hardlinks to one inode = %d, want %d", got, want)
	}
}

func TestCachedDirSizeRespectsInode(t *testing.T) {
	// Isolate the global cache to a test-scratch file so we don't pollute
	// the user's real sizes.json.
	globalCacheMu.Lock()
	globalCache = nil
	globalCachePath = filepath.Join(t.TempDir(), "sizes.json")
	globalCacheMu.Unlock()
	t.Cleanup(func() {
		globalCacheMu.Lock()
		globalCache = nil
		globalCachePath = ""
		globalCacheMu.Unlock()
	})

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}

	// First call: cache miss, computes the real size and stores it.
	if got := CachedDirSize(context.Background(), dir, 2); got != 100 {
		t.Fatalf("first CachedDirSize = %d, want 100", got)
	}

	// Corrupt the cache entry: poison the size and flip the inode so the
	// next call's check sees an inode mismatch. If the H5 guard works,
	// it ignores the poisoned entry and recomputes.
	c := loadGlobalCache()
	c.mu.Lock()
	e := c.entries[dir]
	e.Ino++
	e.Size = 999999
	c.entries[dir] = e
	c.mu.Unlock()

	if got := CachedDirSize(context.Background(), dir, 2); got != 100 {
		t.Errorf("CachedDirSize returned stale value %d after inode mismatch; want recompute = 100", got)
	}
}

func TestDirSizeRespectsContext(t *testing.T) {
	// Build a small tree. Cancellation is what matters, not how big.
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		sub := filepath.Join(dir, "d"+strconv.Itoa(i))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 10; j++ {
			if err := os.WriteFile(filepath.Join(sub, "f"+strconv.Itoa(j)), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Cancel before the call so we don't rely on timing races.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_ = DirSize(ctx, dir, 4)
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Errorf("DirSize with pre-cancelled ctx took %v, expected ~instant", elapsed)
	}
}

func TestLastTouched(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(old, []byte("o"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	recent := filepath.Join(dir, "recent.txt")
	if err := os.WriteFile(recent, []byte("r"), 0o644); err != nil {
		t.Fatal(err)
	}

	lt := LastTouched(dir)
	if time.Since(lt) > time.Minute {
		t.Errorf("LastTouched should reflect recent file; got %v ago", time.Since(lt))
	}
}

func candsSummary(cs []Candidate) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Category.String()+":"+c.Path)
	}
	sort.Strings(out)
	return out
}

