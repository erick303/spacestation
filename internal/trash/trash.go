package trash

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type Result struct {
	Path  string
	Bytes int64
	Err   error
}

// Move sends each path to the macOS Trash via Finder (single batched
// osascript call). Returns one Result per path. If osascript fails the
// caller can retry individual paths or surface per-path errors.
//
// Cancellation: the osascript subprocess is launched via
// exec.CommandContext, so cancelling `ctx` kills it. The batch is atomic
// from Finder's side — partial cancellation may still process some items
// before Finder sees the signal.
func Move(ctx context.Context, paths []string) []Result {
	if len(paths) == 0 {
		return nil
	}

	// Build AppleScript:
	//   tell application "Finder" to delete {POSIX file "/a", POSIX file "/b"}
	var b strings.Builder
	b.WriteString(`tell application "Finder" to delete {`)
	for i, p := range paths {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, `POSIX file %q`, p)
	}
	b.WriteString("}")

	cmd := exec.CommandContext(ctx, "osascript", "-e", b.String())
	out, err := cmd.CombinedOutput()

	results := make([]Result, len(paths))
	for i, p := range paths {
		results[i] = Result{Path: p}
	}
	if err != nil {
		// Batch failed — record error on all entries. Caller may fall back.
		msg := fmt.Errorf("osascript: %w (%s)", err, strings.TrimSpace(string(out)))
		for i := range results {
			results[i].Err = msg
		}
	} else {
		// Verify each path is now gone; if not, mark error.
		for i, p := range paths {
			if _, statErr := os.Stat(p); statErr == nil {
				results[i].Err = fmt.Errorf("path still exists after trash: %s", p)
			}
		}
	}
	return results
}

// Hard removes paths recursively with os.RemoveAll, parallelized across a
// small worker pool. Use only when the user explicitly asked for --hard or
// the trash path failed.
//
// Cancellation: workers check ctx before calling os.RemoveAll, so paths
// not yet started are skipped with the cancel error. An in-flight
// RemoveAll on a particular path runs to completion (os.RemoveAll is not
// context-aware).
func Hard(ctx context.Context, paths []string, workers int) []Result {
	if workers < 1 {
		workers = 4
	}
	results := make([]Result, len(paths))
	for i, p := range paths {
		results[i] = Result{Path: p}
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, p := range paths {
		if ctx.Err() != nil {
			results[i].Err = ctx.Err()
			continue
		}
		i, p := i, p
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := ctx.Err(); err != nil {
				results[i].Err = err
				return
			}
			if err := os.RemoveAll(p); err != nil {
				results[i].Err = err
			}
		}()
	}
	wg.Wait()
	return results
}
