package scan

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLastTouched already covers the common case (max mtime of children) in
// scan_test.go. These tests pin the edge cases that callers rely on:
// missing dir, empty dir, and dir mtime newer than every child.

func TestLastTouchedMissingDirReturnsZero(t *testing.T) {
	got := LastTouched(filepath.Join(t.TempDir(), "does-not-exist"))
	if !got.IsZero() {
		t.Errorf("LastTouched on missing dir = %v, want zero time", got)
	}
}

func TestLastTouchedEmptyDirReturnsRootMtime(t *testing.T) {
	dir := t.TempDir()
	// Force the dir's own mtime to a known recent value so we can detect
	// that LastTouched used it as the fallback rather than returning zero.
	stamp := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(dir, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	got := LastTouched(dir)
	// Filesystems vary on mtime resolution (HFS is 1s, APFS sub-ns) so allow
	// a 2s slop window rather than asserting exact equality.
	if got.Sub(stamp).Abs() > 2*time.Second {
		t.Errorf("LastTouched on empty dir = %v, want ~%v (dir's own mtime)", got, stamp)
	}
}

func TestLastTouchedRootMtimeNewerThanChildren(t *testing.T) {
	dir := t.TempDir()
	// Children are old; the dir itself was just touched (which happens
	// naturally when you create a new entry, but we force-set everything
	// to make the assertion deterministic).
	old := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	for _, name := range []string{"a", "b", "c"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	// LastTouched walks children and returns the max of their mtimes
	// (info.ModTime). Since all children are old but the dir itself was
	// just stat'd as the "best" seed mtime, the result should be the dir's
	// mtime, not "old".
	dirStamp := time.Now().Truncate(time.Second)
	if err := os.Chtimes(dir, dirStamp, dirStamp); err != nil {
		t.Fatal(err)
	}
	got := LastTouched(dir)
	if got.Sub(dirStamp).Abs() > 2*time.Second {
		t.Errorf("LastTouched with all-old children = %v, want ~%v (dir's mtime)", got, dirStamp)
	}
}
