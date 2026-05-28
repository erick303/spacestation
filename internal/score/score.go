package score

import (
	"fmt"
	"time"

	"github.com/erick303/spacestation/internal/config"
	"github.com/erick303/spacestation/internal/scan"
)

// Apply computes a recommendation reason and default-selection for each
// candidate according to the configured selection rules. It mutates the
// slice in place.
func Apply(cs []scan.Candidate, cfg config.Config) {
	now := time.Now()
	minAge := time.Duration(cfg.Selection.DefaultSelectMinAgeDays) * 24 * time.Hour
	dlMinAge := time.Duration(cfg.Selection.DownloadsMinAgeDays) * 24 * time.Hour

	for i := range cs {
		c := &cs[i]
		// Zero mtime means LastTouched() couldn't stat the path (EACCES,
		// vanished mid-scan, sandbox). Treating that as "unknown" rather
		// than "739000 days old" means we never auto-select a directory
		// we couldn't actually read. Trash is the one exception — it's
		// always safe to empty regardless of mtime.
		if c.LastTouched.IsZero() && c.Category != scan.CatTrash {
			c.Reason = "unknown age — not auto-selecting"
			continue
		}
		age := now.Sub(c.LastTouched)
		// Future mtime (clock skew, restored backup, NTP glitch) — clamp
		// so the reason text doesn't render "(-5d)".
		if age < 0 {
			age = 0
		}
		ageDays := int(age / (24 * time.Hour))

		switch {
		case c.Category == scan.CatTrash:
			c.Selected = true
			c.Reason = "Trash — always safe to empty"
		case c.Category == scan.CatDownloads:
			if age > dlMinAge {
				c.Selected = true
				c.Reason = fmt.Sprintf("Untouched for %dd in Downloads", ageDays)
			} else {
				c.Reason = fmt.Sprintf("Recent (%dd) — review manually", ageDays)
			}
		case c.Safety == scan.SafetyRegenerable:
			if age > minAge {
				c.Selected = true
				c.Reason = fmt.Sprintf("Stale %dd, regenerable", ageDays)
			} else {
				c.Reason = fmt.Sprintf("Active (%dd) — may rebuild from scratch if deleted", ageDays)
			}
		default:
			c.Reason = fmt.Sprintf("User content, %dd old", ageDays)
		}
	}
}
