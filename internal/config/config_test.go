package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	// Scan defaults: project_roots includes ~/projects; all probes on.
	if len(cfg.Scan.ProjectRoots) != 1 || cfg.Scan.ProjectRoots[0] != "~/projects" {
		t.Errorf("Default Scan.ProjectRoots = %v, want [~/projects]", cfg.Scan.ProjectRoots)
	}
	if !cfg.Scan.IncludeFixedPaths || !cfg.Scan.IncludeDownloads || !cfg.Scan.IncludeTrash || !cfg.Scan.IncludeSystemCache {
		t.Errorf("Default Scan booleans should all be true, got %+v", cfg.Scan)
	}
	// Selection defaults.
	if cfg.Selection.DefaultSelectMinAgeDays != 30 {
		t.Errorf("Default Selection.DefaultSelectMinAgeDays = %d, want 30", cfg.Selection.DefaultSelectMinAgeDays)
	}
	if cfg.Selection.DownloadsMinAgeDays != 90 {
		t.Errorf("Default Selection.DownloadsMinAgeDays = %d, want 90", cfg.Selection.DownloadsMinAgeDays)
	}
	if cfg.Selection.DownloadsMinSizeMB != 100 {
		t.Errorf("Default Selection.DownloadsMinSizeMB = %d, want 100", cfg.Selection.DownloadsMinSizeMB)
	}
	// Delete defaults to Trash mode — the safer of the two.
	if cfg.Delete.Mode != "trash" {
		t.Errorf("Default Delete.Mode = %q, want %q", cfg.Delete.Mode, "trash")
	}
}

func TestExpand(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"~", home},
		{"~/projects", filepath.Join(home, "projects")},
		{"~/Library/Caches", filepath.Join(home, "Library/Caches")},

		// No leading "~" — passthrough.
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},

		// "~user" (other user's home) is NOT expanded; we only expand bare "~".
		// Verify by checking the result is unchanged rather than asserting
		// what the wrong-behavior would be.
		{"~root/x", "~root/x"},
	}
	for _, tc := range cases {
		if got := Expand(tc.in); got != tc.want {
			t.Errorf("Expand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExpandedRoots(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	cfg := Config{
		Scan: ScanConfig{
			ProjectRoots: []string{"~/projects", "/tmp/work", "~"},
		},
	}
	got := cfg.ExpandedRoots()
	want := []string{
		filepath.Join(home, "projects"),
		"/tmp/work",
		home,
	}
	if len(got) != len(want) {
		t.Fatalf("ExpandedRoots length = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ExpandedRoots[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPath(t *testing.T) {
	got, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "spacestation", "config.toml")
	if got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
	// Sanity: path should at minimum be absolute and end in config.toml.
	if !strings.HasSuffix(got, "config.toml") {
		t.Errorf("Path() = %q, expected suffix config.toml", got)
	}
}
