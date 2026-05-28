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
}

func defaultFixedProbes() []fixedProbe {
	return []fixedProbe{
		{path: "~/Library/Developer/Xcode/DerivedData", cat: CatXcode, safety: SafetyRegenerable,
			detail: "Xcode derived data. Recreated by Xcode on next build."},
		{path: "~/Library/Developer/Xcode/Archives", cat: CatXcode, safety: SafetyUserContent,
			detail: "Xcode build archives. Keep if you may need to symbolicate or re-submit."},
		{path: "~/Library/Developer/CoreSimulator/Devices", cat: CatXcode, safety: SafetyRegenerable,
			detail: "iOS Simulator device state. Recreated by Xcode/simulator on next launch.", expand: true},

		{path: "~/Library/Caches/go-build", cat: CatGoCache, safety: SafetyRegenerable,
			detail: "Go build cache. Recreated by `go build` / `go test`."},
		{path: "~/go/pkg/mod/cache", cat: CatGoCache, safety: SafetyRegenerable,
			detail: "Go module download cache. Recreated by `go mod download`."},

		{path: "~/.cargo/registry", cat: CatRust, safety: SafetyRegenerable,
			detail: "Cargo registry cache. Recreated automatically by cargo."},
		{path: "~/.cargo/git", cat: CatRust, safety: SafetyRegenerable,
			detail: "Cargo git checkouts. Recreated by cargo on next build."},

		{path: "~/.npm/_cacache", cat: CatNodeModules, safety: SafetyRegenerable,
			detail: "npm content-addressable cache. Recreated by `npm install`."},
		{path: "~/.yarn/cache", cat: CatNodeModules, safety: SafetyRegenerable,
			detail: "Yarn cache. Recreated by `yarn install`."},
		{path: "~/Library/pnpm/store", cat: CatNodeModules, safety: SafetyRegenerable,
			detail: "pnpm store. Recreated by `pnpm install`."},

		{path: "~/Library/Caches/Homebrew", cat: CatHomebrew, safety: SafetyRegenerable,
			detail: "Homebrew downloaded bottles. Recreated on next `brew install`."},
		{path: "~/Library/Caches/Homebrew/downloads", cat: CatHomebrew, safety: SafetyRegenerable,
			detail: "Homebrew download cache. Safe to delete; brew will re-download as needed."},

		{path: "~/Library/Containers/com.docker.docker/Data/vms", cat: CatDocker, safety: SafetyUserContent,
			detail: "Docker VM disk image(s). Deleting removes ALL Docker images/volumes/containers."},
		{path: "~/Library/Group Containers/group.com.docker", cat: CatDocker, safety: SafetyUserContent,
			detail: "Docker group container data."},

		{path: "~/Library/Caches", cat: CatSystemCache, safety: SafetyRegenerable,
			detail: "Per-app system caches under ~/Library/Caches. Apps recreate on demand.", expand: true},
		{path: "~/.cache", cat: CatSystemCache, safety: SafetyRegenerable,
			detail: "XDG-style user cache. Apps recreate on demand.", expand: true},
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

	// Skip paths that smart probes will cover — saves redundant DirSize work
	// on huge trees. Smart probes emit better command-based candidates for these.
	skip := smartClaimedPaths()

	var wg sync.WaitGroup
	for _, p := range probes {
		if ctx.Err() != nil {
			break
		}
		full := config.Expand(p.path)
		if skip[full] {
			continue
		}
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			runFixedProbe(ctx, p, workers, skip, emit)
		}()
	}
	wg.Wait()
}

// smartClaimedPaths returns the set of expanded paths that a smart probe
// will cover (if its underlying tool is installed). The fixed-path scanner
// uses this to avoid duplicate sizing work.
func smartClaimedPaths() map[string]bool {
	claimed := map[string]bool{}
	if has("docker") {
		claimed[config.Expand("~/Library/Containers/com.docker.docker/Data/vms")] = true
	}
	if has("brew") {
		claimed[config.Expand("~/Library/Caches/Homebrew")] = true
		claimed[config.Expand("~/Library/Caches/Homebrew/downloads")] = true
	}
	if has("go") {
		claimed[config.Expand("~/Library/Caches/go-build")] = true
		claimed[config.Expand("~/go/pkg/mod/cache")] = true
	}
	if has("npm") {
		claimed[config.Expand("~/.npm/_cacache")] = true
	}
	if has("yarn") {
		claimed[config.Expand("~/.yarn/cache")] = true
	}
	if has("pnpm") {
		claimed[config.Expand("~/Library/pnpm/store")] = true
	}
	if has("xcrun") {
		// CoreSimulator/Devices is "expanded" in the fixed probes (per-child); the
		// smart probe replaces it with a single command candidate. Skip the expand.
		claimed[config.Expand("~/Library/Developer/CoreSimulator/Devices")] = true
	}
	return claimed
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
