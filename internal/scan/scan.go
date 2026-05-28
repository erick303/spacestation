package scan

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	"github.com/erick303/spacestation/internal/config"
)

// Options controls a Scan run.
type Options struct {
	Cfg     config.Config
	Workers int // 0 = NumCPU
}

// Progress is emitted on the progress channel while scanning.
type Progress struct {
	Stage   string // "walking" | "sizing" | "done"
	Message string
	Found   int
	Bytes   int64
}

// Run executes a full scan and returns the discovered candidates.
// `progress` is optional; if non-nil it receives streaming updates.
// The channel is closed by Run when scanning is complete.
func Run(ctx context.Context, opts Options, progress chan<- Progress) []Candidate {
	if opts.Workers == 0 {
		opts.Workers = runtime.NumCPU()
	}
	defer func() {
		if progress != nil {
			close(progress)
		}
	}()

	sendProgress := func(p Progress) {
		if progress == nil {
			return
		}
		select {
		case progress <- p:
		default:
		}
	}

	var (
		mu         sync.Mutex
		candidates []Candidate
		totalBytes int64
	)

	addCandidate := func(c Candidate) {
		if c.Title == "" {
			c.Title = c.Path
		}
		mu.Lock()
		candidates = append(candidates, c)
		totalBytes += c.SizeBytes
		count := len(candidates)
		bytes := totalBytes
		mu.Unlock()
		sendProgress(Progress{Stage: "found", Message: c.Path, Found: count, Bytes: bytes})
	}

	// Run project walks and fixed-path probes in parallel.
	var topWG sync.WaitGroup

	for _, root := range opts.Cfg.ExpandedRoots() {
		root := root
		topWG.Add(1)
		go func() {
			defer topWG.Done()
			sendProgress(Progress{Stage: "walking", Message: root})
			walkProjects(ctx, root, opts.Workers, addCandidate)
		}()
	}

	if opts.Cfg.Scan.IncludeFixedPaths {
		topWG.Add(1)
		go func() {
			defer topWG.Done()
			sendProgress(Progress{Stage: "walking", Message: "fixed paths"})
			probeFixedPaths(ctx, opts.Cfg, opts.Workers, addCandidate)
		}()
		topWG.Add(1)
		go func() {
			defer topWG.Done()
			sendProgress(Progress{Stage: "walking", Message: "ecosystem cleanups"})
			probeSmart(ctx, opts.Cfg, addCandidate)
		}()
	}

	if opts.Cfg.Scan.IncludeDownloads {
		topWG.Add(1)
		go func() {
			defer topWG.Done()
			sendProgress(Progress{Stage: "walking", Message: "Downloads"})
			probeDownloads(ctx, opts.Cfg, opts.Workers, addCandidate)
		}()
	}

	if opts.Cfg.Scan.IncludeTrash {
		topWG.Add(1)
		go func() {
			defer topWG.Done()
			probeTrash(ctx, opts.Workers, addCandidate)
		}()
	}

	topWG.Wait()

	// Dedupe by path: keep the most useful candidate.
	// Preference: ActionCommand > more-specific category (lower SortOrder).
	// This lets a smart probe (e.g. "brew cleanup") replace the dumb path
	// probe (~/Library/Caches/Homebrew) when both target the same dir.
	{
		byPath := map[string]int{}
		out := candidates[:0]
		preferNew := func(a, b Candidate) bool {
			if a.Action != b.Action {
				return a.Action == ActionCommand
			}
			return a.Category.SortOrder() < b.Category.SortOrder()
		}
		for _, c := range candidates {
			if existing, ok := byPath[c.Path]; ok {
				if preferNew(c, out[existing]) {
					out[existing] = c
				}
				continue
			}
			byPath[c.Path] = len(out)
			out = append(out, c)
		}
		candidates = out
		totalBytes = 0
		for _, c := range candidates {
			totalBytes += c.SizeBytes
		}
	}

	// Stable sort: by category sort order, then by size desc.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Category != candidates[j].Category {
			return candidates[i].Category.SortOrder() < candidates[j].Category.SortOrder()
		}
		return candidates[i].SizeBytes > candidates[j].SizeBytes
	})

	sendProgress(Progress{Stage: "done", Found: len(candidates), Bytes: totalBytes})
	return candidates
}

// walkProjects performs a concurrent pattern-walk over `root`. When a known
// build/cache directory is encountered it is captured as a Candidate and
// NOT descended into — this is the key perf win.
func walkProjects(ctx context.Context, root string, workers int, emit func(Candidate)) {
	if _, err := os.Stat(root); err != nil {
		return
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	var walk func(p string)
	walk = func(p string) {
		defer wg.Done()
		select {
		case <-ctx.Done():
			return
		default:
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if shouldSkipWalk(name) {
				continue
			}
			full := filepath.Join(p, name)
			info, err := e.Info()
			if err != nil {
				continue
			}
			// Skip symlinks to avoid loops & duplicate counting.
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if cat, detail, matched := classifyDir(name); matched {
				size := CachedDirSize(ctx, full, workers)
				if size == 0 {
					continue
				}
				emit(Candidate{
					Path:        full,
					Category:    cat,
					SizeBytes:   size,
					LastTouched: LastTouched(full),
					Safety:      SafetyRegenerable,
					Detail:      detail,
				})
				continue // do NOT descend
			}
			wg.Add(1)
			select {
			case sem <- struct{}{}:
				go func(path string) {
					walk(path)
					<-sem
				}(full)
			default:
				walk(full)
			}
		}
	}

	wg.Add(1)
	sem <- struct{}{}
	go func() {
		walk(root)
		<-sem
	}()
	wg.Wait()
}
