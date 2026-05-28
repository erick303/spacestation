package cleanup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// moveToTrash sends each path to the macOS Trash via Finder (single batched
// osascript call). Returns one error per path, in input order — nil for
// success.
//
// Cancellation: the osascript subprocess is launched via exec.CommandContext,
// so cancelling ctx kills it. The batch is atomic from Finder's side —
// partial cancellation may still process some items before Finder sees the
// signal.
func moveToTrash(ctx context.Context, paths []string) []error {
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

	errs := make([]error, len(paths))
	if err != nil {
		// Batch failed — record error on all entries. Caller may fall back.
		msg := fmt.Errorf("osascript: %w (%s)", err, strings.TrimSpace(string(out)))
		for i := range errs {
			errs[i] = msg
		}
		return errs
	}
	// Verify each path is now gone; if not, mark error.
	for i, p := range paths {
		if _, statErr := os.Stat(p); statErr == nil {
			errs[i] = fmt.Errorf("path still exists after trash: %s", p)
		}
	}
	return errs
}

// hardDelete removes paths recursively with os.RemoveAll, parallelized
// across a small worker pool. Use only when the user explicitly asked for
// --hard or the trash path failed.
//
// Cancellation: workers check ctx before calling os.RemoveAll, so paths not
// yet started are skipped with the cancel error. An in-flight RemoveAll on
// a particular path runs to completion (os.RemoveAll is not context-aware).
func hardDelete(ctx context.Context, paths []string, workers int) []error {
	if workers < 1 {
		workers = 4
	}
	errs := make([]error, len(paths))

	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, p := range paths {
		if ctx.Err() != nil {
			errs[i] = ctx.Err()
			continue
		}
		i, p := i, p
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := ctx.Err(); err != nil {
				errs[i] = err
				return
			}
			if err := os.RemoveAll(p); err != nil {
				errs[i] = err
			}
		}()
	}
	wg.Wait()
	return errs
}
