package scan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
			detail: "Go build cache. Recreated by `go build` / `go test`.",
			replacedBy: []string{"go"}},
		{path: "~/go/pkg/mod/cache", cat: CatGoCache, safety: SafetyRegenerable,
			detail: "Go module download cache. Recreated by `go mod download`.",
			replacedBy: []string{"go"}},

		{path: "~/.cargo/registry", cat: CatRust, safety: SafetyRegenerable,
			detail: "Cargo registry cache. Recreated automatically by cargo."},
		{path: "~/.cargo/git", cat: CatRust, safety: SafetyRegenerable,
			detail: "Cargo git checkouts. Recreated by cargo on next build."},

		{path: "~/.npm/_cacache", cat: CatNodeModules, safety: SafetyRegenerable,
			detail: "npm content-addressable cache. Recreated by `npm install`.",
			replacedBy: []string{"npm"}},
		{path: "~/.yarn/cache", cat: CatNodeModules, safety: SafetyRegenerable,
			detail: "Yarn cache. Recreated by `yarn install`.",
			replacedBy: []string{"yarn"}},
		{path: "~/Library/pnpm/store", cat: CatNodeModules, safety: SafetyRegenerable,
			detail: "pnpm store. Recreated by `pnpm install`.",
			replacedBy: []string{"pnpm"}},

		{path: "~/Library/Caches/Homebrew", cat: CatHomebrew, safety: SafetyRegenerable,
			detail: "Homebrew downloaded bottles. Recreated on next `brew install`.",
			replacedBy: []string{"brew"}},
		{path: "~/Library/Caches/Homebrew/downloads", cat: CatHomebrew, safety: SafetyRegenerable,
			detail: "Homebrew download cache. Safe to delete; brew will re-download as needed.",
			replacedBy: []string{"brew"}},

		{path: "~/Library/Containers/com.docker.docker/Data/vms", cat: CatDocker, safety: SafetyUserContent,
			detail: "Docker VM disk image(s). Deleting removes ALL Docker images/volumes/containers.",
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

func probeFixedPaths(ctx context.Context, cfg config.Config, workers int, emit func(Candidate)) {
	probes := defaultFixedProbes()

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
		// Honor the include_system_caches knob: when false, skip every
		// CatSystemCache probe (~/Library/Caches, ~/.cache, ~/Library/Logs).
		// All other categories are always probed.
		if p.cat == CatSystemCache && !cfg.Scan.IncludeSystemCache {
			continue
		}
		if claimed[config.Expand(p.path)] {
			continue
		}
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			runFixedProbe(ctx, p, workers, claimed, emit)
		}()
	}
	wg.Wait()
}

// hasAny reports whether any of the named tools is on PATH.
func hasAny(tools []string) bool {
	for _, t := range tools {
		if has(t) {
			return true
		}
	}
	return false
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
	var childWG sync.WaitGroup
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
		go func(path string) {
			defer childWG.Done()
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
				Detail:      "Item in ~/Downloads. User content — deletion is permanent (when --hard) or moves to Trash.",
			})
		}(full)
	}
	wg.Wait()
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
