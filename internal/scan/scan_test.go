package scan

import (
	"context"
	"os"
	"path/filepath"
	"sort"
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
		path := filepath.Join(dir, "f"+itoa(i))
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

func TestDirSizeRespectsContext(t *testing.T) {
	// Build a small tree. Cancellation is what matters, not how big.
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		sub := filepath.Join(dir, "d"+itoa(i))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 10; j++ {
			if err := os.WriteFile(filepath.Join(sub, "f"+itoa(j)), []byte("x"), 0o644); err != nil {
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

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b strings.Builder
	neg := i < 0
	if neg {
		i = -i
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if neg {
		b.WriteByte('-')
	}
	b.Write(digits)
	return b.String()
}
