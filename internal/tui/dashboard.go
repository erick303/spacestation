package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/erick303/spacestation/internal/scan"
)

const (
	leftPad     = "  "
	rightMargin = 4  // empty cols at the right edge so bars visibly stop short
	labelWidth  = 14 // visual indent for the "label    bar/text" pattern
)

// renderDashboard returns the read-only disk overview, top to bottom: the macOS
// disk numbers (used %, free, reclaimable), the disk fullness bar (full width),
// the reclaim-mix stacked bar colored per category, and the per-category
// breakdown that wraps across as many lines as needed.
//
// width is the available terminal columns. Bars degrade gracefully when narrow.
func renderDashboard(width int, du scan.DiskUsage, cands []scan.Candidate) string {
	if width <= 0 {
		width = 100
	}
	inner := width - len(leftPad) - rightMargin

	barW := max(inner-labelWidth-2, 20) // ▕…▏

	reclaimable := totalCandidateBytes(cands)

	line1 := leftPad + renderDiskText(du, reclaimable)
	line2 := leftPad + renderDiskBar(barW, du, reclaimable)
	line3 := leftPad + renderReclaimBar(barW, cands)
	line4 := renderBreakdown(width-len(leftPad)-rightMargin, cands)

	return line1 + "\n" + line2 + "\n" + line3 + "\n" + line4
}

// renderDiskText is the text row above the disk bar.
func renderDiskText(du scan.DiskUsage, reclaimable int64) string {
	label := mutedStyle.Render(padRight("macOS disk", labelWidth))
	if du.Total <= 0 {
		return label + mutedStyle.Render("(disk usage unavailable)")
	}
	pct := int(float64(du.Used) / float64(du.Total) * 100)
	sep := mutedStyle.Render("  ·  ")
	usedPart := fmt.Sprintf("%s used (%d%% of %s)", humanBytes(du.Used), pct, humanBytes(du.Total))
	freePart := sizeStyle.Render(humanBytes(du.Free)) + " free"
	reclPart := sizeStyle.Render(humanBytes(reclaimable)) + " reclaimable"
	return label + usedPart + sep + freePart + sep + reclPart
}

func totalCandidateBytes(cs []scan.Candidate) int64 {
	var n int64
	for _, c := range cs {
		n += c.SizeBytes
	}
	return n
}

type catBytes struct {
	cat   scan.Category
	bytes int64
}

// sortedCategoryBytes groups candidates by category and returns the per-category
// byte totals, largest first. Shared by the reclaim-mix bar and the breakdown.
func sortedCategoryBytes(cands []scan.Candidate) []catBytes {
	byCat := map[scan.Category]int64{}
	for _, c := range cands {
		byCat[c.Category] += c.SizeBytes
	}
	entries := make([]catBytes, 0, len(byCat))
	for cat, b := range byCat {
		entries = append(entries, catBytes{cat, b})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].bytes > entries[j].bytes })
	return entries
}

// renderDiskBar — three segments inside one bar:
//
//	cyan  = used non-reclaimable
//	green = used and reclaimable
//	muted = free
func renderDiskBar(barWidth int, du scan.DiskUsage, reclaimable int64) string {
	label := strings.Repeat(" ", labelWidth) // align under the disk-text row above
	if du.Total <= 0 {
		return label + mutedStyle.Render("—")
	}

	usedNonRecl := du.Used - reclaimable
	if usedNonRecl < 0 {
		usedNonRecl = 0
		reclaimable = du.Used
	}

	cyanW := proportion(barWidth, usedNonRecl, du.Total)
	greenW := proportion(barWidth, reclaimable, du.Total)
	freeW := max(barWidth-cyanW-greenW, 0)

	cyanStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#7DCFFF"))
	bar := cyanStyle.Render(strings.Repeat("█", cyanW)) +
		sizeStyle.Render(strings.Repeat("█", greenW)) +
		mutedStyle.Render(strings.Repeat("░", freeW))

	return label + "▕" + bar + "▏"
}

// renderReclaimBar — stacked proportional segments per category.
func renderReclaimBar(barWidth int, cands []scan.Candidate) string {
	label := mutedStyle.Render(padRight("reclaim mix", labelWidth))
	if len(cands) == 0 {
		return label + mutedStyle.Render("(nothing reclaimable)")
	}

	entries := sortedCategoryBytes(cands)
	total := totalCandidateBytes(cands)

	if total == 0 {
		return label + mutedStyle.Render("(nothing reclaimable)")
	}

	var sb strings.Builder
	allocated := 0
	for i, e := range entries {
		var w int
		if i == len(entries)-1 {
			w = barWidth - allocated
		} else {
			w = proportion(barWidth, e.bytes, total)
		}
		if w < 0 {
			w = 0
		}
		allocated += w
		if w > 0 {
			sb.WriteString(categoryStyle(e.cat).Render(strings.Repeat("█", w)))
		}
	}
	return label + "▕" + sb.String() + "▏"
}

// renderBreakdown — color-coded list of categories with sizes, largest first.
// Wraps across multiple lines when the categories don't fit on one row, with
// each continuation line indented to align under the first entry.
func renderBreakdown(termWidth int, cands []scan.Candidate) string {
	label := mutedStyle.Render(padRight("breakdown", labelWidth))
	indent := strings.Repeat(" ", labelWidth)
	if len(cands) == 0 {
		return leftPad + label + mutedStyle.Render("—")
	}
	entries := sortedCategoryBytes(cands)

	sep := mutedStyle.Render(" · ")
	sepW := lipgloss.Width(sep) // ANSI-aware; will be 3 for " · ".

	maxLineW := max(termWidth, labelWidth+10)

	var lines []string
	var line strings.Builder
	line.WriteString(leftPad)
	line.WriteString(label)
	lineW := len(leftPad) + labelWidth
	firstOnLine := true

	for _, e := range entries {
		txt := fmt.Sprintf("%s %s", e.cat.String(), humanBytes(e.bytes))
		txtW := lipgloss.Width(txt) // category names are ASCII today; future-proof anyway.
		need := txtW
		if !firstOnLine {
			need += sepW
		}
		if lineW+need > maxLineW && !firstOnLine {
			// wrap to a new continuation line, no separator at start
			lines = append(lines, line.String())
			line.Reset()
			line.WriteString(leftPad)
			line.WriteString(indent)
			lineW = len(leftPad) + labelWidth
			firstOnLine = true
		}
		if !firstOnLine {
			line.WriteString(sep)
			lineW += sepW
		}
		line.WriteString(categoryStyle(e.cat).Render(txt))
		lineW += txtW
		firstOnLine = false
	}
	lines = append(lines, line.String())
	return strings.Join(lines, "\n")
}

// proportion returns round(width * num / denom) clamped to >= 0.
func proportion(width int, num, denom int64) int {
	if denom <= 0 || width <= 0 {
		return 0
	}
	return max(int(int64(width)*num/denom), 0)
}
