package scan

import (
	"os"
	"time"
)

// LastTouched returns the most recent mtime among immediate children of root.
// Falls back to root's own mtime if it's empty or unreadable.
func LastTouched(root string) time.Time {
	info, err := os.Stat(root)
	if err != nil {
		return time.Time{}
	}
	best := info.ModTime()

	entries, err := os.ReadDir(root)
	if err != nil {
		return best
	}
	for _, e := range entries {
		ci, err := e.Info()
		if err != nil {
			continue
		}
		if ci.ModTime().After(best) {
			best = ci.ModTime()
		}
	}
	return best
}
