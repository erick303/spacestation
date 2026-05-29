package scan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/erick303/spacestation/internal/config"
)

type fixedProbe struct {
	path   string // ~-relative; expanded by Expand()
	cat    Category
	safety Safety
	detail string
	expand bool // whether to glob/list children individually

	// replacedBy: if any of these tools is on PATH, a smart probe in
	// smart.go will emit a better (typically ActionCommand) candidate for
	// this location, so this fixed probe is suppressed. The matching path
	// is also added to the expand-loop's claimed set so an expand parent
	// (e.g. ~/Library/Caches) doesn't re-emit it as a child.
	replacedBy []string
}

func defaultFixedProbes() []fixedProbe {
	return []fixedProbe{
		{path: "~/Library/Developer/Xcode/DerivedData", cat: CatXcode, safety: SafetyRegenerable,
			detail: "Xcode derived data. Recreated by Xcode on next build."},
		{path: "~/Library/Developer/Xcode/Archives", cat: CatXcode, safety: SafetyUserContent,
			detail: "Xcode build archives. Keep if you may need to symbolicate or re-submit."},
		{path: "~/Library/Developer/CoreSimulator/Devices", cat: CatXcode, safety: SafetyRegenerable,
			detail: "iOS Simulator device state. Recreated by Xcode/simulator on next launch.", expand: true,
			replacedBy: []string{"xcrun"}},
		{path: "~/Library/Developer/Xcode/iOS DeviceSupport", cat: CatXcode, safety: SafetyRegenerable,
			detail: "Per-iOS-version debug symbols. Re-downloaded when you connect a device.", expand: true},
		{path: "~/Library/Developer/Xcode/watchOS DeviceSupport", cat: CatXcode, safety: SafetyRegenerable,
			detail: "Per-watchOS-version debug symbols. Re-downloaded when you connect a device.", expand: true},
		{path: "~/Library/Developer/Xcode/tvOS DeviceSupport", cat: CatXcode, safety: SafetyRegenerable,
			detail: "Per-tvOS-version debug symbols. Re-downloaded when you connect a device.", expand: true},

		{path: "~/Library/Caches/go-build", cat: CatGoCache, safety: SafetyRegenerable,
			detail:     "Go build cache. Recreated by `go build` / `go test`.",
			replacedBy: []string{"go"}},
		{path: "~/go/pkg/mod/cache", cat: CatGoCache, safety: SafetyRegenerable,
			detail:     "Go module download cache. Recreated by `go mod download`.",
			replacedBy: []string{"go"}},

		{path: "~/.cargo/registry", cat: CatRust, safety: SafetyRegenerable,
			detail: "Cargo registry cache. Recreated automatically by cargo."},
		{path: "~/.cargo/git", cat: CatRust, safety: SafetyRegenerable,
			detail: "Cargo git checkouts. Recreated by cargo on next build."},

		{path: "~/.npm/_cacache", cat: CatNodeModules, safety: SafetyRegenerable,
			detail:     "npm content-addressable cache. Recreated by `npm install`.",
			replacedBy: []string{"npm"}},
		{path: "~/.yarn/cache", cat: CatNodeModules, safety: SafetyRegenerable,
			detail:     "Yarn cache. Recreated by `yarn install`.",
			replacedBy: []string{"yarn"}},
		{path: "~/Library/pnpm/store", cat: CatNodeModules, safety: SafetyRegenerable,
			detail:     "pnpm store. Recreated by `pnpm install`.",
			replacedBy: []string{"pnpm"}},

		{path: "~/Library/Caches/Homebrew", cat: CatHomebrew, safety: SafetyRegenerable,
			detail:     "Homebrew downloaded bottles. Recreated on next `brew install`.",
			replacedBy: []string{"brew"}},
		{path: "~/Library/Caches/Homebrew/downloads", cat: CatHomebrew, safety: SafetyRegenerable,
			detail:     "Homebrew download cache. Safe to delete; brew will re-download as needed.",
			replacedBy: []string{"brew"}},

		{path: "~/Library/Containers/com.docker.docker/Data/vms", cat: CatDocker, safety: SafetyUserContent,
			detail:     "Docker VM disk image(s). Deleting removes ALL Docker images/volumes/containers.",
			replacedBy: []string{"docker"}},
		{path: "~/Library/Group Containers/group.com.docker", cat: CatDocker, safety: SafetyUserContent,
			detail: "Docker group container data."},

		{path: "~/Library/Caches", cat: CatSystemCache, safety: SafetyRegenerable,
			detail: "Per-app system caches under ~/Library/Caches. Apps recreate on demand.", expand: true},
		{path: "~/.cache", cat: CatSystemCache, safety: SafetyRegenerable,
			detail: "XDG-style user cache. Apps recreate on demand.", expand: true},
		{path: "~/Library/Logs", cat: CatSystemCache, safety: SafetyRegenerable,
			detail: "App and diagnostic logs. Apps recreate as needed.", expand: true},
	}
}

// Location is a known fixed scan target, surfaced in the settings UI so the
// user can toggle individual targets on or off.
type Location struct {
	Path   string // ~-relative; expanded by config.Expand
	Label  string // category name, e.g. "xcode", "docker"
	Detail string // human description, reused as the per-item help text
}

// DefaultLocations returns the static catalog of known fixed locations, in
// display order, deduped by path. probeFixedPaths skips any whose path appears
// in cfg.Scan.DisabledLocations.
func DefaultLocations() []Location {
	var out []Location
	seen := map[string]bool{}
	for _, p := range defaultFixedProbes() {
		if seen[p.path] {
			continue
		}
		seen[p.path] = true
		out = append(out, Location{Path: p.path, Label: p.cat.String(), Detail: p.detail})
	}
	return out
}

func probeFixedPaths(ctx context.Context, cfg config.Config, workers int, emit func(Candidate)) {
	probes := defaultFixedProbes()

	// Locations the user turned off in the setup screen.
	disabledLoc := map[string]bool{}
	for _, d := range cfg.Scan.DisabledLocations {
		disabledLoc[config.Expand(d)] = true
	}

	// Add brew --cache if brew is on PATH and not already covered.
	if out, err := exec.Command("brew", "--cache").Output(); err == nil {
		p := strings.TrimSpace(string(out))
		if p != "" && p != config.Expand("~/Library/Caches/Homebrew") {
			probes = append(probes, fixedProbe{
				path: p, cat: CatHomebrew, safety: SafetyRegenerable,
				detail: "Homebrew cache (reported by `brew --cache`).",
			})
		}
	}

	// Derive the set of paths that smart probes will claim, from the
	// probe metadata itself. A probe with replacedBy is suppressed when
	// any named tool is on PATH; its path also goes into the claimed set
	// so that an expand parent (e.g. ~/Library/Caches) doesn't re-emit it
	// as a child. The same path can't drift between two different lists
	// because there's only one list — the probes themselves.
	claimed := map[string]bool{}
	for _, p := range probes {
		if hasAny(p.replacedBy) {
			claimed[config.Expand(p.path)] = true
		}
	}

	var wg sync.WaitGroup
	for _, p := range probes {
		if ctx.Err() != nil {
			break
		}
		// Skip locations the user turned off in the setup screen.
		if disabledLoc[config.Expand(p.path)] {
			continue
		}
		if claimed[config.Expand(p.path)] {
			continue
		}
		p := p
		wg.Go(func() {
			runFixedProbe(ctx, p, workers, claimed, emit)
		})
	}
	wg.Wait()
}

// hasAny reports whether any of the named tools is on PATH.
func hasAny(tools []string) bool {
	return slices.ContainsFunc(tools, has)
}

func runFixedProbe(ctx context.Context, p fixedProbe, workers int, skip map[string]bool, emit func(Candidate)) {
	full := config.Expand(p.path)
	info, err := os.Stat(full)
	if err != nil {
		return
	}
	if !info.IsDir() {
		return
	}

	if !p.expand {
		size := CachedDirSize(ctx, full, workers)
		if size == 0 {
			return
		}
		emit(Candidate{
			Path:        full,
			Category:    p.cat,
			SizeBytes:   size,
			LastTouched: LastTouched(full),
			Safety:      p.safety,
			Detail:      p.detail,
		})
		return
	}

	// Expand: emit one Candidate per child, so user can pick.
	entries, err := os.ReadDir(full)
	if err != nil {
		return
	}
	// Bound child concurrency. Without this, ~/Library/Caches with N children
	// (often 100–300) would spawn N goroutines, each calling CachedDirSize
	// with its own pool of `workers` walker goroutines — peak ~N×workers
	// concurrent FDs/syscalls. Cap fan-out at `workers` so we get
	// workers × workers as the worst case instead.
	var childWG sync.WaitGroup
	childSem := make(chan struct{}, workers)
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		if !e.IsDir() {
			continue
		}
		child := filepath.Join(full, e.Name())
		if skip[child] {
			continue
		}
		childWG.Add(1)
		childSem <- struct{}{}
		go func(path string) {
			defer childWG.Done()
			defer func() { <-childSem }()
			size := CachedDirSize(ctx, path, workers)
			if size == 0 {
				return
			}
			emit(Candidate{
				Path:        path,
				Category:    p.cat,
				SizeBytes:   size,
				LastTouched: LastTouched(path),
				Safety:      p.safety,
				Detail:      p.detail,
			})
		}(child)
	}
	childWG.Wait()
}

func probeDownloads(ctx context.Context, cfg config.Config, workers int, emit func(Candidate)) {
	root := config.Expand("~/Downloads")
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	minSize := cfg.Selection.DownloadsMinSizeMB * 1024 * 1024
	var wg sync.WaitGroup
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join(root, e.Name())
		wg.Add(1)
		go func(full string) {
			defer wg.Done()
			info, err := os.Lstat(full)
			if err != nil {
				return
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return
			}
			var size int64
			if info.IsDir() {
				size = CachedDirSize(ctx, full, workers)
			} else if info.Mode().IsRegular() {
				size = info.Size()
			}
			if size < minSize {
				return
			}
			emit(Candidate{
				Path:        full,
				Category:    CatDownloads,
				SizeBytes:   size,
				LastTouched: info.ModTime(),
				Safety:      SafetyUserContent,
				Detail:      "Item in ~/Downloads. User content — moves to Trash.",
			})
		}(full)
	}
	wg.Wait()
}

// screenshotExts are the image extensions macOS `screencapture` can write.
// The default is .png; the format is user-configurable to the others.
var screenshotExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".heic": true, ".tiff": true, ".pdf": true,
}

// isScreenshotName reports whether name looks like a macOS screenshot using the
// default naming scheme ("Screenshot 2026-01-12 at 09.43.30.png", including the
// " (2)" duplicate suffix). Matching is by prefix + extension only — renamed
// screenshots are intentionally left alone, since the name is our sole signal.
func isScreenshotName(name string) bool {
	if !strings.HasPrefix(name, "Screenshot ") {
		return false
	}
	return screenshotExts[strings.ToLower(filepath.Ext(name))]
}

// probeScreenshots emits one candidate per macOS screenshot sitting in the
// configured screenshot directory (default ~/Desktop). Screenshots are user
// content, not regenerable — they move to Trash, and scoring only auto-selects
// ones past the age gate.
func probeScreenshots(ctx context.Context, _ config.Config, emit func(Candidate)) {
	root := config.ScreenshotDir()
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		if e.IsDir() || !isScreenshotName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		emit(Candidate{
			Path:        filepath.Join(root, e.Name()),
			Category:    CatScreenshots,
			SizeBytes:   info.Size(),
			LastTouched: info.ModTime(),
			Safety:      SafetyUserContent,
			Detail:      "Screenshot saved by macOS. User content — moves to Trash.",
		})
	}
}

func probeTrash(ctx context.Context, workers int, emit func(Candidate)) {
	root := config.Expand("~/.Trash")
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	// One candidate per top-level item, mirroring probeDownloads. Each item is
	// its own row so the user can browse the Trash and remove items selectively;
	// the "empty Trash" path (cleanup.EmptyTrash) wipes everything, including the
	// dot-prefixed entries we skip for display here.
	var wg sync.WaitGroup
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join(root, e.Name())
		wg.Add(1)
		go func(full, name string) {
			defer wg.Done()
			info, err := os.Lstat(full)
			if err != nil {
				return
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return
			}
			var size int64
			if info.IsDir() {
				size = CachedDirSize(ctx, full, workers)
			} else if info.Mode().IsRegular() {
				size = info.Size()
			}
			emit(Candidate{
				Path:        full,
				Title:       name,
				Category:    CatTrash,
				SizeBytes:   size,
				LastTouched: info.ModTime(),
				Safety:      SafetyUserContent,
				Detail:      "Item in the Trash — removing it is permanent (press x).",
			})
		}(full, e.Name())
	}
	wg.Wait()
}
