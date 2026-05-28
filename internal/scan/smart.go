package scan

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/erick303/spacestation/internal/config"
)

// probeSmart runs all per-ecosystem "intelligent" probes in parallel.
// Each probe checks whether its tool exists; if not, it returns and the
// existing fixed-path probe (in fixed.go) covers the dumb fallback.
func probeSmart(ctx context.Context, _ config.Config, emit func(Candidate)) {
	var wg sync.WaitGroup
	probes := []func(context.Context, func(Candidate)){
		probeDockerSmart,
		probeBrewSmart,
		probeGoSmart,
		probeNpmSmart,
		probeYarnSmart,
		probePnpmSmart,
		probeCargoSmart,
		probeUvSmart,
		probePipSmart,
		probeXcodeSimulatorsSmart,
	}
	for _, p := range probes {
		if ctx.Err() != nil {
			break
		}
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			p(ctx, emit)
		}()
	}
	wg.Wait()
}

// runWithTimeout runs cmd with a hard timeout, derived from `parent` so the
// command is also cancelled when the parent scan is cancelled.
func runWithTimeout(parent context.Context, name string, args []string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

func has(tool string) bool {
	_, err := exec.LookPath(tool)
	return err == nil
}

// ---------- Docker ----------

type dockerDFEntry struct {
	Type        string `json:"Type"`
	Reclaimable string `json:"Reclaimable"`
	Size        string `json:"Size"`
}

var reclaimableRe = regexp.MustCompile(`^([0-9.]+)\s*([A-Za-z]+)`)

func parseDockerSize(s string) int64 {
	m := reclaimableRe.FindStringSubmatch(s)
	if len(m) < 3 {
		return 0
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	switch strings.ToUpper(m[2]) {
	case "B":
		return int64(n)
	case "KB":
		return int64(n * 1024)
	case "MB":
		return int64(n * 1024 * 1024)
	case "GB":
		return int64(n * 1024 * 1024 * 1024)
	case "TB":
		return int64(n * 1024 * 1024 * 1024 * 1024)
	}
	return 0
}

func probeDockerSmart(ctx context.Context, emit func(Candidate)) {
	if !has("docker") {
		return
	}
	out, err := runWithTimeout(ctx, "docker", []string{"system", "df", "--format", "{{json .}}"}, 6*time.Second)
	if err != nil {
		return
	}
	var reclaim int64
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var e dockerDFEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		reclaim += parseDockerSize(e.Reclaimable)
	}
	if reclaim <= 0 {
		return
	}
	emit(Candidate{
		Path:        "docker://system-prune",
		Title:       "Docker: prune unused images, containers, volumes, build cache",
		Category:    CatDocker,
		SizeBytes:   reclaim,
		LastTouched: time.Now(),
		Safety:      SafetyRegenerable,
		Detail:      "Runs `docker system prune -a --volumes -f`. Removes stopped containers, unused images, unused volumes, and the entire build cache. Does NOT shrink Docker Desktop's VM disk image — for that use Docker Desktop > Troubleshoot > Clean / Purge Data.",
		Action:      ActionCommand,
		Command:     []string{"docker", "system", "prune", "-a", "--volumes", "-f"},
	})
}

// ---------- Homebrew ----------

func probeBrewSmart(ctx context.Context, emit func(Candidate)) {
	if !has("brew") {
		return
	}
	out, err := runWithTimeout(ctx, "brew", []string{"cleanup", "--dry-run", "-s", "--prune=all"}, 10*time.Second)
	if err != nil {
		return
	}
	// Lines look like: "Would remove: /path/to/file (1.2GB)"
	var reclaim int64
	sizeRe := regexp.MustCompile(`\(([0-9.]+)\s*([A-Za-z]+)\)`)
	for _, line := range strings.Split(string(out), "\n") {
		m := sizeRe.FindStringSubmatch(line)
		if len(m) == 3 {
			reclaim += parseDockerSize(m[1] + m[2])
		}
	}
	if reclaim <= 0 {
		// brew may have nothing to clean — still useful info, skip
		return
	}
	emit(Candidate{
		Path:        "brew://cleanup",
		Title:       "Homebrew: cleanup old versions and downloads",
		Category:    CatHomebrew,
		SizeBytes:   reclaim,
		LastTouched: time.Now(),
		Safety:      SafetyRegenerable,
		Detail:      "Runs `brew cleanup -s --prune=all`. Removes old versions of installed kegs and the entire download cache. Safe: brew re-downloads on demand.",
		Action:      ActionCommand,
		Command:     []string{"brew", "cleanup", "-s", "--prune=all"},
	})
}

// ---------- Go ----------

func probeGoSmart(ctx context.Context, emit func(Candidate)) {
	if !has("go") {
		return
	}
	// Size the build cache and modcache directly (Go has no "dry run" for clean).
	buildCache := config.Expand("~/Library/Caches/go-build")
	if fi, err := os.Stat(buildCache); err == nil && fi.IsDir() {
		size := CachedDirSize(ctx, buildCache, 4)
		if size > 0 {
			emit(Candidate{
				Path:        buildCache,
				Title:       "Go: clean build cache",
				Category:    CatGoCache,
				SizeBytes:   size,
				LastTouched: LastTouched(buildCache),
				Safety:      SafetyRegenerable,
				Detail:      "Runs `go clean -cache`. Removes the go build cache (~/Library/Caches/go-build). Recreated on next `go build`/`go test`.",
				Action:      ActionCommand,
				Command:     []string{"go", "clean", "-cache"},
			})
		}
	}
	modCache := config.Expand("~/go/pkg/mod/cache")
	if fi, err := os.Stat(modCache); err == nil && fi.IsDir() {
		size := CachedDirSize(ctx, modCache, 4)
		if size > 0 {
			emit(Candidate{
				Path:        modCache,
				Title:       "Go: clean module download cache",
				Category:    CatGoCache,
				SizeBytes:   size,
				LastTouched: LastTouched(modCache),
				Safety:      SafetyRegenerable,
				Detail:      "Runs `go clean -modcache`. Removes ~/go/pkg/mod. Re-downloaded by `go mod download` on next build.",
				Action:      ActionCommand,
				Command:     []string{"go", "clean", "-modcache"},
			})
		}
	}
}

// ---------- npm ----------

func probeNpmSmart(ctx context.Context, emit func(Candidate)) {
	if !has("npm") {
		return
	}
	cacheDir := config.Expand("~/.npm/_cacache")
	fi, err := os.Stat(cacheDir)
	if err != nil || !fi.IsDir() {
		return
	}
	size := CachedDirSize(ctx, cacheDir, 4)
	if size <= 0 {
		return
	}
	emit(Candidate{
		Path:        cacheDir,
		Title:       "npm: cache clean",
		Category:    CatNodeModules,
		SizeBytes:   size,
		LastTouched: LastTouched(cacheDir),
		Safety:      SafetyRegenerable,
		Detail:      "Runs `npm cache clean --force`. Empties ~/.npm/_cacache. Recreated on next install.",
		Action:      ActionCommand,
		Command:     []string{"npm", "cache", "clean", "--force"},
	})
}

// ---------- yarn ----------

func probeYarnSmart(ctx context.Context, emit func(Candidate)) {
	if !has("yarn") {
		return
	}
	cacheDir := config.Expand("~/.yarn/cache")
	if fi, err := os.Stat(cacheDir); err != nil || !fi.IsDir() {
		// Yarn berry uses a different location; just exit if classic dir missing.
		return
	}
	size := CachedDirSize(ctx, cacheDir, 4)
	if size <= 0 {
		return
	}
	emit(Candidate{
		Path:        cacheDir,
		Title:       "yarn: cache clean",
		Category:    CatNodeModules,
		SizeBytes:   size,
		LastTouched: LastTouched(cacheDir),
		Safety:      SafetyRegenerable,
		Detail:      "Runs `yarn cache clean`. Empties ~/.yarn/cache. Recreated on next install.",
		Action:      ActionCommand,
		Command:     []string{"yarn", "cache", "clean"},
	})
}

// ---------- pnpm ----------

func probePnpmSmart(ctx context.Context, emit func(Candidate)) {
	if !has("pnpm") {
		return
	}
	// `pnpm store path` gives the store location.
	out, err := runWithTimeout(ctx, "pnpm", []string{"store", "path"}, 5*time.Second)
	if err != nil {
		return
	}
	storePath := strings.TrimSpace(string(out))
	if storePath == "" {
		return
	}
	if fi, err := os.Stat(storePath); err != nil || !fi.IsDir() {
		return
	}
	size := CachedDirSize(ctx, storePath, 4)
	if size <= 0 {
		return
	}
	// `pnpm store prune` removes only unreferenced packages — typically reclaims
	// 50-90% without slowing future installs. Estimate conservatively at 70%.
	estimate := size * 7 / 10
	emit(Candidate{
		Path:        storePath,
		Title:       "pnpm: prune unreferenced packages (keeps current installs fast)",
		Category:    CatNodeModules,
		SizeBytes:   estimate,
		LastTouched: LastTouched(storePath),
		Safety:      SafetyRegenerable,
		Detail:      "Runs `pnpm store prune`. Removes only packages not referenced by any project on this machine. Does NOT empty the store wholesale; your active installs stay fast. Estimate is ~70% of store size (~" + humanBytesShort(size) + " total).",
		Action:      ActionCommand,
		Command:     []string{"pnpm", "store", "prune"},
	})
}

// ---------- Cargo (Rust) ----------

func probeCargoSmart(ctx context.Context, emit func(Candidate)) {
	// `cargo cache` (the third-party tool) is the smart option; without it,
	// the safe move is to keep the dumb-path probe.
	if !has("cargo-cache") {
		return
	}
	// cargo-cache --autoclean removes stale registry/git checkouts.
	registry := config.Expand("~/.cargo/registry")
	gitCache := config.Expand("~/.cargo/git")
	var size int64
	for _, d := range []string{registry, gitCache} {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			size += CachedDirSize(ctx, d, 4)
		}
	}
	if size <= 0 {
		return
	}
	emit(Candidate{
		Path:        registry,
		Title:       "cargo: autoclean stale registry & git checkouts",
		Category:    CatRust,
		SizeBytes:   size / 2, // autoclean is conservative — estimate ~50%
		LastTouched: LastTouched(registry),
		Safety:      SafetyRegenerable,
		Detail:      "Runs `cargo cache --autoclean`. Removes stale registry sources and git checkouts. Estimate ~50% of cache.",
		Action:      ActionCommand,
		Command:     []string{"cargo", "cache", "--autoclean"},
	})
}

// ---------- uv (Python) ----------

func probeUvSmart(ctx context.Context, emit func(Candidate)) {
	if !has("uv") {
		return
	}
	cacheDir := config.Expand("~/.cache/uv")
	if fi, err := os.Stat(cacheDir); err != nil || !fi.IsDir() {
		return
	}
	size := CachedDirSize(ctx, cacheDir, 4)
	if size <= 0 {
		return
	}
	emit(Candidate{
		Path:        cacheDir,
		Title:       "uv: cache clean",
		Category:    CatPython,
		SizeBytes:   size,
		LastTouched: LastTouched(cacheDir),
		Safety:      SafetyRegenerable,
		Detail:      "Runs `uv cache clean`. Empties uv's package cache. Recreated on next install.",
		Action:      ActionCommand,
		Command:     []string{"uv", "cache", "clean"},
	})
}

// ---------- pip (Python) ----------

func probePipSmart(ctx context.Context, emit func(Candidate)) {
	if !has("pip") && !has("pip3") {
		return
	}
	pip := "pip"
	if !has("pip") {
		pip = "pip3"
	}
	// Ask pip where its cache lives.
	out, err := runWithTimeout(ctx, pip, []string{"cache", "dir"}, 5*time.Second)
	if err != nil {
		return
	}
	cacheDir := strings.TrimSpace(string(out))
	if cacheDir == "" {
		return
	}
	if fi, err := os.Stat(cacheDir); err != nil || !fi.IsDir() {
		return
	}
	size := CachedDirSize(ctx, cacheDir, 4)
	if size <= 0 {
		return
	}
	emit(Candidate{
		Path:        cacheDir,
		Title:       "pip: cache purge",
		Category:    CatPython,
		SizeBytes:   size,
		LastTouched: LastTouched(cacheDir),
		Safety:      SafetyRegenerable,
		Detail:      "Runs `" + pip + " cache purge`. Empties pip's wheel cache. Recreated on next install.",
		Action:      ActionCommand,
		Command:     []string{pip, "cache", "purge"},
	})
}

// ---------- Xcode simulators ----------

func probeXcodeSimulatorsSmart(ctx context.Context, emit func(Candidate)) {
	if !has("xcrun") {
		return
	}
	// Estimate reclaim by sizing unavailable runtime directories. xcrun simctl
	// list devices --json gives availability per device.
	out, err := runWithTimeout(ctx, "xcrun", []string{"simctl", "list", "devices", "unavailable", "--json"}, 8*time.Second)
	if err != nil {
		return
	}
	var data struct {
		Devices map[string][]struct {
			UDID string `json:"udid"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return
	}
	devicesDir := config.Expand("~/Library/Developer/CoreSimulator/Devices")
	var size int64
	for _, list := range data.Devices {
		for _, d := range list {
			p := filepath.Join(devicesDir, d.UDID)
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				size += CachedDirSize(ctx, p, 4)
			}
		}
	}
	if size <= 0 {
		return
	}
	emit(Candidate{
		Path:        "xcrun://simctl-delete-unavailable",
		Title:       "Xcode: delete unavailable simulators (xcrun simctl)",
		Category:    CatXcode,
		SizeBytes:   size,
		LastTouched: time.Now(),
		Safety:      SafetyRegenerable,
		Detail:      "Runs `xcrun simctl delete unavailable`. Removes simulator devices that point to runtimes you no longer have installed. Safe — these can't run anyway.",
		Action:      ActionCommand,
		Command:     []string{"xcrun", "simctl", "delete", "unavailable"},
	})
}

// humanBytesShort: small helper for embedding in detail strings.
func humanBytesShort(n int64) string {
	const (
		mb = 1024 * 1024
		gb = 1024 * mb
	)
	switch {
	case n >= gb:
		return strconv.FormatFloat(float64(n)/float64(gb), 'f', 1, 64) + " GB"
	case n >= mb:
		return strconv.FormatFloat(float64(n)/float64(mb), 'f', 0, 64) + " MB"
	default:
		return strconv.FormatInt(n, 10) + " B"
	}
}
