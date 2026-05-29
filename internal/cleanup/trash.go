package cleanup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
