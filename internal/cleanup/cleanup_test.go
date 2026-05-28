package cleanup

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// buildTrashTree lays out a small Trash-like tree under dir and returns the
// number of top-level entries and the total number of files+dirs it contains
// (i.e. how many unlinks a full removal performs).
func buildTrashTree(t *testing.T, dir string) (topLevel, entries int) {
	t.Helper()
	// A top-level file.
	mustWrite(t, filepath.Join(dir, "loose.txt"), "x")
	// A nested directory with files and a subdir.
	mustWrite(t, filepath.Join(dir, "cache", "a", "1.bin"), "aa")
	mustWrite(t, filepath.Join(dir, "cache", "a", "2.bin"), "bb")
	mustWrite(t, filepath.Join(dir, "cache", "b", "3.bin"), "cc")
	// Another top-level dir with one file.
	mustWrite(t, filepath.Join(dir, "logs", "app.log"), "log")

	// Top-level entries: loose.txt, cache, logs.
	// Unlinks: loose.txt(1) + cache,cache/a,cache/a/1,cache/a/2,cache/b,cache/b/3(6) + logs,logs/app.log(2) = 9.
	return 3, 9
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestEmptyTrashRemovesAllAndReportsProgress(t *testing.T) {
	dir := t.TempDir()
	wantTop, wantEntries := buildTrashTree(t, dir)

	var (
		mu       sync.Mutex
		maxDone  int
		maxFiles int
		gotTotal int
		nonEmpty bool
	)
	progress := func(p TrashProgress) {
		mu.Lock()
		defer mu.Unlock()
		nonEmpty = true
		if p.Done > maxDone {
			maxDone = p.Done
		}
		if p.Files > maxFiles {
			maxFiles = p.Files
		}
		if p.Total > 0 {
			gotTotal = p.Total
		}
	}

	if err := EmptyTrash(context.Background(), dir, progress); err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}

	// The dir itself survives; its contents are gone.
	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir after empty: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("expected empty Trash dir, still has %d entries", len(left))
	}

	if !nonEmpty {
		t.Fatal("progress callback never fired")
	}
	if gotTotal != wantTop {
		t.Errorf("Total = %d, want %d top-level entries", gotTotal, wantTop)
	}
	if maxDone != wantTop {
		t.Errorf("max Done = %d, want %d (bar should reach total)", maxDone, wantTop)
	}
	// The final item-completion events carry an accurate file snapshot, so the
	// high-water mark must equal every unlink performed.
	if maxFiles != wantEntries {
		t.Errorf("max Files = %d, want %d unlinks", maxFiles, wantEntries)
	}
}

func TestRemoveTreeCountingCountsEveryEntry(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "tree")
	mustWrite(t, filepath.Join(root, "x", "1"), "1")
	mustWrite(t, filepath.Join(root, "x", "2"), "2")
	mustWrite(t, filepath.Join(root, "y"), "3")
	// Entries under root: root, x, x/1, x/2, y = 5.

	var count int
	err := removeTreeCounting(context.Background(), root, func(string) { count++ })
	if err != nil {
		t.Fatalf("removeTreeCounting: %v", err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Errorf("tree not fully removed: %v", err)
	}
	if count != 5 {
		t.Errorf("onFile fired %d times, want 5", count)
	}
}

func TestRemoveTreeCountingMissingPathIsNoError(t *testing.T) {
	dir := t.TempDir()
	if err := removeTreeCounting(context.Background(), filepath.Join(dir, "nope"), nil); err != nil {
		t.Errorf("removing missing path: %v", err)
	}
}

func TestRemoveTreeCountingCancelled(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "tree")
	mustWrite(t, filepath.Join(root, "a", "1"), "1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := removeTreeCounting(ctx, root, nil); err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
