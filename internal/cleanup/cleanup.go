package cleanup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/erick303/spacestation/internal/scan"
	"github.com/erick303/spacestation/internal/trash"
)

type Result struct {
	Candidate scan.Candidate
	Output    string // captured stdout/stderr for command actions
	Err       error
}

// Mode controls how delete-action candidates are removed.
type Mode int

const (
	ModeTrash Mode = iota // move to ~/.Trash via Finder
	ModeHard              // rm -rf
)

// Execute runs the right cleanup for each candidate:
//   - ActionDelete: batched move-to-Trash (or hard delete if mode == ModeHard).
//   - ActionCommand: run the ecosystem's cleanup tool with a hard timeout.
//
// Returns one Result per input candidate, in input order.
//
// Cancellation: if `ctx` is cancelled mid-run, the currently-running
// subprocess is killed (via exec.CommandContext) and remaining items are
// marked with the cancellation error. The Trash batch and Hard workers
// also observe ctx between paths.
func Execute(ctx context.Context, cands []scan.Candidate, mode Mode) []Result {
	results := make([]Result, len(cands))
	for i, c := range cands {
		results[i] = Result{Candidate: c}
	}

	// Group delete actions for batched osascript. The Trash candidate is
	// special-cased — you can't "move ~/.Trash to the Trash", so we empty its
	// contents with RemoveAll regardless of mode.
	var deletePaths []string
	var deleteIdx []int
	for i, c := range cands {
		if c.Action != scan.ActionDelete {
			continue
		}
		if c.Category == scan.CatTrash {
			// Trash is driven by the separate empty/remove action
			// (RemoveFromTrash / EmptyTrash) — never the move-to-Trash path.
			// Defensive: skip any that leak in so we never try to "move
			// ~/.Trash to the Trash".
			continue
		}
		deletePaths = append(deletePaths, c.Path)
		deleteIdx = append(deleteIdx, i)
	}

	if len(deletePaths) > 0 && ctx.Err() == nil {
		var delResults []trash.Result
		if mode == ModeHard {
			delResults = trash.Hard(ctx, deletePaths, 8)
		} else {
			// Trash mode is honest: per-path failures stay failures.
			// The confirm hint promises Trash, so we never escalate to
			// RemoveAll. If the user wants that, they re-run with --hard.
			delResults = trash.Move(ctx, deletePaths)
		}
		for j, r := range delResults {
			i := deleteIdx[j]
			results[i].Err = r.Err
		}
	}

	// Command actions run sequentially — they may be heavy (docker prune,
	// go clean -modcache), and serial output is easier for the user to read.
	for i, c := range cands {
		if c.Action != scan.ActionCommand {
			continue
		}
		if err := ctx.Err(); err != nil {
			results[i].Err = err
			continue
		}
		out, err := runCommand(ctx, c.Command, 5*time.Minute)
		results[i].Output = out
		results[i].Err = err
	}
	return results
}

// TrashProgress reports progress while permanently removing Trash items.
// Two kinds of event share this struct, distinguished only by which counters
// moved since the last one:
//
//   - file ticks: emitted as individual files are unlinked while a big item is
//     being cleared, so the UI shows motion (and a streaming log) instead of
//     freezing on one entry for minutes. Files climbs; Done does not.
//   - item completions: emitted once per top-level item as it finishes. Done
//     climbs (1..Total).
//
// Every event carries an atomic snapshot of all counters, so the UI can just
// take the max of each — dropped/out-of-order events never corrupt the totals.
type TrashProgress struct {
	Path  string // the file or item just removed (newest first in the UI log)
	Done  int    // top-level items finished so far
	Total int    // total top-level items in this removal
	Files int    // individual files/dirs unlinked so far across the whole op
	Err   error  // non-nil if a top-level item failed to remove
}

// progressEvery throttles file-tick emissions: a non-empty Trash can hold
// hundreds of thousands of files, and we don't need a UI event for each. Every
// Nth unlink is plenty for a smooth counter + log. Item completions are never
// throttled, so the count and bar always reach their totals.
const progressEvery = 64

// RemoveFromTrash permanently removes each candidate's Path, in parallel, by
// unlinking file-by-file (see removeTreeCounting) so progress streams even
// inside one huge item. Items already in the Trash can't be re-trashed, so
// removal is always permanent regardless of delete mode. Returns one Result per
// input candidate, in input order, so the same done-screen / size-cache code
// path applies.
//
// progress, if non-nil, is called for file ticks and per item as it finishes.
// It may be called concurrently from worker goroutines, so it must be safe for
// that.
func RemoveFromTrash(ctx context.Context, cands []scan.Candidate, progress func(TrashProgress)) []Result {
	results := make([]Result, len(cands))
	for i, c := range cands {
		results[i] = Result{Candidate: c}
	}
	var (
		wg    sync.WaitGroup
		done  int64
		files int64
	)
	total := len(cands)
	sem := make(chan struct{}, 8)
	for i, c := range cands {
		if ctx.Err() != nil {
			results[i].Err = ctx.Err()
			continue
		}
		i, p := i, c.Path
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := ctx.Err(); err != nil {
				results[i].Err = err
				return
			}
			err := removeTreeCounting(ctx, p, fileTicker(&files, &done, total, progress))
			if err != nil {
				results[i].Err = err
			}
			if progress != nil {
				n := atomic.AddInt64(&done, 1)
				progress(TrashProgress{Path: p, Done: int(n), Total: total, Files: int(atomic.LoadInt64(&files)), Err: err})
			}
		}()
	}
	wg.Wait()
	return results
}

// EmptyTrash removes all contents of `dir` (typically ~/.Trash), including
// hidden entries, without removing the dir itself. Thin wrapper over emptyTrash.
//
// progress, if non-nil, is called once per top-level entry as it finishes; see
// RemoveFromTrash for concurrency notes.
func EmptyTrash(ctx context.Context, dir string, progress func(TrashProgress)) error {
	return emptyTrash(ctx, dir, progress)
}

// emptyTrash removes the contents of `dir` (typically ~/.Trash) without
// removing the dir itself. Errors on individual entries are collected and
// returned as a single joined error; per-item permission-denied is common
// (e.g. items the OS itself has locked) and shouldn't fail the whole op.
func emptyTrash(ctx context.Context, dir string, progress func(TrashProgress)) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		errs  []string
		done  int64
		files int64
	)
	total := len(entries)
	sem := make(chan struct{}, 8)
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		full := filepath.Join(dir, e.Name())
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			rmErr := removeTreeCounting(ctx, p, fileTicker(&files, &done, total, progress))
			if rmErr != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("%s: %v", filepath.Base(p), rmErr))
				mu.Unlock()
			}
			if progress != nil {
				n := atomic.AddInt64(&done, 1)
				progress(TrashProgress{Path: p, Done: int(n), Total: total, Files: int(atomic.LoadInt64(&files)), Err: rmErr})
			}
		}(full)
	}
	wg.Wait()
	if len(errs) == 0 {
		return nil
	}
	if len(errs) > 5 {
		return fmt.Errorf("%d items in Trash could not be removed: %s, …", len(errs), strings.Join(errs[:5], "; "))
	}
	return fmt.Errorf("%d items in Trash could not be removed: %s", len(errs), strings.Join(errs, "; "))
}

// fileTicker builds the per-file callback passed to removeTreeCounting. It
// bumps the shared file counter on every unlink but only emits a (throttled)
// progress event every progressEvery files, snapshotting all counters so the
// UI sees a consistent picture. Returns nil when there's nothing to report to,
// so removeTreeCounting can skip the call entirely.
func fileTicker(files, done *int64, total int, progress func(TrashProgress)) func(string) {
	if progress == nil {
		return nil
	}
	return func(p string) {
		n := atomic.AddInt64(files, 1)
		if n%progressEvery == 0 {
			progress(TrashProgress{Path: p, Done: int(atomic.LoadInt64(done)), Total: total, Files: int(n)})
		}
	}
}

// removeTreeCounting removes path and everything under it, calling onFile after
// each individual file/dir is unlinked. It's the per-file analogue of
// os.RemoveAll — which is itself a recursive unlink, so the extra callback adds
// negligible cost — but lets the caller stream progress through a large tree
// instead of blocking opaquely on one RemoveAll. Best-effort like RemoveAll: it
// returns the first error encountered but keeps going where it can. onFile may
// be nil. ctx cancellation aborts between entries.
func removeTreeCounting(ctx context.Context, path string, onFile func(string)) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var firstErr error
	// Recurse into real directories only — never follow symlinks (Lstat, and
	// the IsDir check on the link's own mode, keep us from deleting outside
	// the tree).
	if info.IsDir() {
		entries, rerr := os.ReadDir(path)
		if rerr != nil {
			firstErr = rerr
		}
		for _, e := range entries {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if cerr := removeTreeCounting(ctx, filepath.Join(path, e.Name()), onFile); cerr != nil && firstErr == nil {
				firstErr = cerr
			}
		}
	}
	if rerr := os.Remove(path); rerr != nil {
		if firstErr == nil {
			firstErr = rerr
		}
		return firstErr
	}
	if onFile != nil {
		onFile(path)
	}
	return firstErr
}

func runCommand(parent context.Context, cmd []string, timeout time.Duration) (string, error) {
	if len(cmd) == 0 {
		return "", fmt.Errorf("empty command")
	}
	// Derive the timeout from the parent ctx so a user-side cancel kills
	// the subprocess immediately rather than waiting up to `timeout`.
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	out, err := c.CombinedOutput()
	// Truncate very long output — we just need a sample for the UI.
	s := string(out)
	if len(s) > 4096 {
		s = s[:4096] + "\n…(truncated)"
	}
	if err != nil {
		return s, fmt.Errorf("%s: %w", strings.Join(cmd, " "), err)
	}
	return s, nil
}
