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

// TrashProgress reports the removal of a single Trash item. It is emitted once
// per item as it finishes (success or failure), so the UI can show a running
// count and a log of what just went. Done is the number of items finished so
// far (1..Total); Total is fixed for the run.
type TrashProgress struct {
	Path  string // the item just removed (or attempted)
	Done  int    // items finished so far, including this one
	Total int    // total items in this removal
	Err   error  // non-nil if this item failed to remove
}

// RemoveFromTrash permanently removes each candidate's Path (os.RemoveAll), in
// parallel. Items already in the Trash can't be re-trashed, so removal is always
// permanent regardless of delete mode. Returns one Result per input candidate,
// in input order, so the same done-screen / size-cache code path applies.
//
// progress, if non-nil, is called once per item as it finishes. It may be
// called concurrently from worker goroutines, so it must be safe for that.
func RemoveFromTrash(ctx context.Context, cands []scan.Candidate, progress func(TrashProgress)) []Result {
	results := make([]Result, len(cands))
	for i, c := range cands {
		results[i] = Result{Candidate: c}
	}
	var (
		wg   sync.WaitGroup
		done int64
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
			err := os.RemoveAll(p)
			if err != nil {
				results[i].Err = err
			}
			if progress != nil {
				n := atomic.AddInt64(&done, 1)
				progress(TrashProgress{Path: p, Done: int(n), Total: total, Err: err})
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
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []string
		done int64
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
			rmErr := os.RemoveAll(p)
			if rmErr != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("%s: %v", filepath.Base(p), rmErr))
				mu.Unlock()
			}
			if progress != nil {
				n := atomic.AddInt64(&done, 1)
				progress(TrashProgress{Path: p, Done: int(n), Total: total, Err: rmErr})
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
