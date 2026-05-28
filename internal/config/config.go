package config

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
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
