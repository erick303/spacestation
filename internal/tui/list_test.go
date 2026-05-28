package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/erick303/spacestation/internal/scan"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// modelWithItems builds a browsing model with perGroup candidates in each of
// many categories, so group-separator blanks are sprinkled throughout the list
// — including inside the bottom viewport, which is what made the old row-based
// window drop the cursor.
func modelWithItems(perGroup int) *model {
	cats := []scan.Category{
		scan.CatNodeModules, scan.CatJSBuild, scan.CatPython, scan.CatRust,
		scan.CatJVM, scan.CatGoCache, scan.CatXcode, scan.CatHomebrew,
		scan.CatDocker, scan.CatSystemCache, scan.CatDownloads, scan.CatTrash,
	}
	var cands []scan.Candidate
	n := 0
	for _, cat := range cats {
		for range perGroup {
			n++
			cands = append(cands, scan.Candidate{
				Title:     fmt.Sprintf("item-%03d", n),
				Category:  cat,
				SizeBytes: int64(1000 - n),
			})
		}
	}
	m := &model{
		cands:     cands,
		collapsed: map[scan.Category]bool{},
		width:     120,
		scanDone:  true,
	}
	m.rebuildRows()
	return m
}

// TestRenderListAlwaysShowsCursor guards the windowing fix: at every cursor
// position the cursor's row must appear in the rendered viewport. The old
// row-based window let group-separator blanks eat the height budget and drop
// the bottom/cursor row off-screen.
func TestRenderListAlwaysShowsCursor(t *testing.T) {
	lipgloss.SetColorProfile(0) // no ANSI, keep assertions simple
	m := modelWithItems(4)      // 12 groups × 4 = many separators throughout
	const height = 12

	for cursor := range m.rows {
		m.cursor = cursor
		r := m.rows[cursor]
		if r.isHeader {
			continue // headers are exercised indirectly; assert on item rows
		}
		title := m.cands[r.candIdx].DisplayTitle()
		out := stripANSI(m.renderList(m.width, height))

		if !strings.Contains(out, title) {
			t.Fatalf("cursor=%d (%q) not visible in viewport:\n%s", cursor, title, out)
		}
		// The viewport must never exceed its height budget (it ends with a
		// trailing newline, hence the -1).
		if got := strings.Count(out, "\n"); got > height {
			t.Fatalf("cursor=%d: viewport emitted %d lines, budget %d", cursor, got, height)
		}
	}
}

func TestRenderListShortListNoWindowing(t *testing.T) {
	lipgloss.SetColorProfile(0)
	m := modelWithItems(1) // 12 groups × 1 item = 24 rows, well under the budget
	out := stripANSI(m.renderList(m.width, 40))
	for i := 1; i <= 3; i++ {
		if !strings.Contains(out, fmt.Sprintf("item-%03d", i)) {
			t.Errorf("short list missing item-%03d:\n%s", i, out)
		}
	}
}
