package config

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Scan      ScanConfig      `toml:"scan"`
	Selection SelectionConfig `toml:"selection"`
	Delete    DeleteConfig    `toml:"delete"`
}

type ScanConfig struct {
	ProjectRoots       []string `toml:"project_roots"`
	IncludeFixedPaths  bool     `toml:"include_fixed_paths"`
	IncludeDownloads   bool     `toml:"include_downloads"`
	IncludeTrash       bool     `toml:"include_trash"`
	IncludeSystemCache bool     `toml:"include_system_caches"`
	IncludeScreenshots bool     `toml:"include_screenshots"`
}

type SelectionConfig struct {
	DefaultSelectMinAgeDays int   `toml:"default_select_min_age_days"`
	DownloadsMinAgeDays     int   `toml:"downloads_min_age_days"`
	DownloadsMinSizeMB      int64 `toml:"downloads_min_size_mb"`
	ScreenshotsMinAgeDays   int   `toml:"screenshots_min_age_days"`
}

type DeleteConfig struct {
	Mode string `toml:"mode"` // "trash" | "hard"
}

func Default() Config {
	return Config{
		Scan: ScanConfig{
			ProjectRoots:       []string{"~/projects"},
			IncludeFixedPaths:  true,
			IncludeDownloads:   true,
			IncludeTrash:       true,
			IncludeSystemCache: true,
			IncludeScreenshots: true,
		},
		Selection: SelectionConfig{
			DefaultSelectMinAgeDays: 30,
			DownloadsMinAgeDays:     90,
			DownloadsMinSizeMB:      100,
			ScreenshotsMinAgeDays:   90,
		},
		Delete: DeleteConfig{Mode: "trash"},
	}
}

func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "spacestation", "config.toml"), nil
}

func Load() (Config, string, error) {
	path, err := Path()
	if err != nil {
		return Config{}, "", err
	}
	cfg := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if werr := writeDefault(path, cfg); werr != nil {
			return cfg, path, nil
		}
		return cfg, path, nil
	}
	if err != nil {
		return cfg, path, err
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return cfg, path, err
	}
	return cfg, path, nil
}

func writeDefault(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(commented(cfg)), 0o644)
}

// configTmpl renders the config as TOML with an explanatory comment above every
// key. The encoder can't emit comments, so we drive a template off the struct —
// values stay bound to cfg (never drifting from Default()) while the prose is
// fixed. `quote` produces a TOML-safe double-quoted string.
var configTmpl = template.Must(template.New("config").
	Funcs(template.FuncMap{"quote": strconv.Quote}).
	Parse(`# spacestation configuration.
# Auto-created on first run — edit freely. CLI flags override these values.

[scan]
# Directories walked for project artifact dirs (node_modules, target, dist, …).
# A leading "~" expands to your home directory.
project_roots = [{{range $i, $r := .Scan.ProjectRoots}}{{if $i}}, {{end}}{{quote $r}}{{end}}]
# Probe well-known fixed locations (Xcode DerivedData, Docker, ~/.cargo, …).
include_fixed_paths = {{.Scan.IncludeFixedPaths}}
# Include old, large files sitting in ~/Downloads.
include_downloads = {{.Scan.IncludeDownloads}}
# Include the contents of ~/.Trash.
include_trash = {{.Scan.IncludeTrash}}
# Include per-app system caches under ~/Library/Caches and ~/.cache.
include_system_caches = {{.Scan.IncludeSystemCache}}
# Include macOS screenshots in your configured screenshot location.
include_screenshots = {{.Scan.IncludeScreenshots}}

[selection]
# Pre-select regenerable items untouched for at least this many days.
default_select_min_age_days = {{.Selection.DefaultSelectMinAgeDays}}
# Only pre-select ~/Downloads items at least this old (days)…
downloads_min_age_days = {{.Selection.DownloadsMinAgeDays}}
# …and at least this large (MB). Smaller downloads are listed but not pre-selected.
downloads_min_size_mb = {{.Selection.DownloadsMinSizeMB}}
# Only pre-select screenshots at least this old (days).
screenshots_min_age_days = {{.Selection.ScreenshotsMinAgeDays}}

[delete]
# How "enter" cleans: "trash" moves to ~/.Trash (restorable), "hard" deletes permanently.
mode = {{quote .Delete.Mode}}
`))

// commented renders cfg as commented TOML via configTmpl.
func commented(cfg Config) string {
	var b bytes.Buffer
	// The template and cfg shape are both fixed, so Execute cannot fail here.
	_ = configTmpl.Execute(&b, cfg)
	return b.String()
}

// Expand replaces a leading "~" with the user's home dir.
func Expand(p string) string {
	if p == "" {
		return p
	}
	if p == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

func (c Config) ExpandedRoots() []string {
	out := make([]string, 0, len(c.Scan.ProjectRoots))
	for _, r := range c.Scan.ProjectRoots {
		out = append(out, Expand(r))
	}
	return out
}

// ScreenshotDir returns the directory macOS saves screenshots to. It reads the
// user's `com.apple.screencapture location` default and falls back to ~/Desktop
// when that key is unset, malformed, or `defaults` is unavailable (e.g. on a
// non-macOS host or in a sandbox). The returned path is always tilde-expanded.
func ScreenshotDir() string {
	fallback := Expand("~/Desktop")
	out, err := exec.Command("defaults", "read", "com.apple.screencapture", "location").Output()
	if err != nil {
		return fallback
	}
	loc := strings.TrimSpace(string(out))
	if loc == "" {
		return fallback
	}
	return Expand(loc)
}
