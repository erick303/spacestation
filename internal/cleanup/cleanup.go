package cleanup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
func Execute(cands []scan.Candidate, mode Mode) []Result {
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
			results[i].Err = emptyTrash(c.Path)
			continue
		}
		deletePaths = append(deletePaths, c.Path)
		deleteIdx = append(deleteIdx, i)
	}

	if len(deletePaths) > 0 {
		var delResults []trash.Result
		if mode == ModeHard {
			delResults = trash.Hard(deletePaths, 8)
		} else {
			// Trash mode is honest: per-path failures stay failures.
			// The confirm hint promises Trash, so we never escalate to
			// RemoveAll. If the user wants that, they re-run with --hard.
			delResults = trash.Move(deletePaths)
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
		out, err := runCommand(c.Command, 5*time.Minute)
		results[i].Output = out
		results[i].Err = err
	}
	return results
}

// emptyTrash removes the contents of `dir` (typically ~/.Trash) without
// removing the dir itself. Errors on individual entries are collected and
// returned as a single joined error; per-item permission-denied is common
// (e.g. items the OS itself has locked) and shouldn't fail the whole op.
func emptyTrash(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []string
	)
	sem := make(chan struct{}, 8)
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := os.RemoveAll(p); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("%s: %v", filepath.Base(p), err))
				mu.Unlock()
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

func runCommand(cmd []string, timeout time.Duration) (string, error) {
	if len(cmd) == 0 {
		return "", fmt.Errorf("empty command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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
