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
	cardWidth   = 18 // inner content width of each stat card
)

var (
	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorMuted).
			Padding(0, 1).
			Width(cardWidth).
			MarginRight(2)

	cardLabelStyle = lipgloss.NewStyle().Foreground(colorMuted)
	cyanValueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#7DCFFF")).Bold(true)
	freeValueStyle = lipgloss.NewStyle().Bold(true)
)

// renderDashboard returns the read-only disk overview, top to bottom:
//  1. header — volume name · filesystem · capacity
//  2. a full-width disk fullness bar (used / reclaimable / free)
//  3. three stat cards — used, free, reclaimable
//  4. the "By Category" reclaim-mix bar and a single-row breakdown
//
// width is the available terminal columns. Bars degrade gracefully when narrow.
func renderDashboard(width int, du scan.DiskUsage, cands []scan.Candidate) string {
	if width <= 0 {
		width = 100
	}
	inner := width - len(leftPad) - rightMargin

	reclaimable := totalCandidateBytes(cands)

	diskBarW := max(inner-2, 20) // ▕…▏
	reclaimBarW := max(inner-labelWidth, 20)

	header := leftPad + renderDiskHeader(du)
	diskBar := leftPad + renderDiskBar(diskBarW, du, reclaimable)
	cards := indentBlock(renderStatCards(du, reclaimable), leftPad)
	reclaimBar := leftPad + renderReclaimBar(reclaimBarW, cands)
	breakdown := renderBreakdown(inner, cands)

	return strings.Join([]string{
		header,
		"",
		diskBar,
		"",
		cards,
		"",
		reclaimBar,
		breakdown,
	}, "\n")
}

// renderDiskHeader is the title row: bold volume name, then a muted subtitle of
// filesystem type and total capacity. Falls back to a generic name off macOS.
func renderDiskHeader(du scan.DiskUsage) string {
	name := du.VolumeName
	if name == "" {
		name = "Disk"
	}
	out := lipgloss.NewStyle().Bold(true).Render(name)

	var sub []string
	if du.FSType != "" {
		sub = append(sub, du.FSType)
	}
	if du.Total > 0 {
		sub = append(sub, humanBytes(du.Total))
	}
	if len(sub) > 0 {
		out += mutedStyle.Render("  · " + strings.Join(sub, " · "))
	}
	return out
}

// renderStatCards lays the three headline numbers into bordered widgets joined
// side by side.
func renderStatCards(du scan.DiskUsage, reclaimable int64) string {
	used := statCard("Used", humanBytes(du.Used), pctOf(du.Used, du.Total), cyanValueStyle)
	free := statCard("Free", humanBytes(du.Free), pctOf(du.Free, du.Total), freeValueStyle)
	recl := statCard("Reclaimable", humanBytes(reclaimable), pctOf(reclaimable, du.Total), sizeStyle)
	row := lipgloss.JoinHorizontal(lipgloss.Top, used, free, recl)
	// Drop the trailing margin the last card leaves on every line.
	lines := strings.Split(row, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

func statCard(label, value, sub string, valueStyle lipgloss.Style) string {
	inner := cardLabelStyle.Render(label) + "\n" +
		valueStyle.Render(value) + "\n" +
		cardLabelStyle.Render(sub)
	return cardStyle.Render(inner)
}

// pctOf renders "N% of disk", or "" when total is unknown.
func pctOf(n, total int64) string {
	if total <= 0 {
		return ""
	}
	return fmt.Sprintf("%d%% of disk", int(float64(n)/float64(total)*100))
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

// renderDiskBar — three segments inside one full-width bar:
//
//	cyan  = used non-reclaimable
//	green = used and reclaimable
//	muted = free
func renderDiskBar(barWidth int, du scan.DiskUsage, reclaimable int64) string {
	if du.Total <= 0 {
		return mutedStyle.Render("(disk usage unavailable)")
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

	return mutedStyle.Render("▕") + bar + mutedStyle.Render("▏")
}

// renderReclaimBar — labeled, full-width stacked bar with one segment per
// category, sized in proportion to its share of the reclaimable total.
func renderReclaimBar(barWidth int, cands []scan.Candidate) string {
	label := mutedStyle.Render(padRight("By Category", labelWidth))
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
	return label + sb.String()
}

// renderBreakdown — color-coded category list, largest first, capped to a
// single row. When the categories don't all fit it shows as many as it can and
// appends "+N more" rather than wrapping into a ragged second line.
func renderBreakdown(maxW int, cands []scan.Candidate) string {
	entries := sortedCategoryBytes(cands)
	if len(entries) == 0 {
		return leftPad + mutedStyle.Render("—")
	}

	const sep = " · "
	sepW := len(sep)

	row := leftPad
	rowW := len(leftPad)
	placed := 0
	for i, e := range entries {
		txt := fmt.Sprintf("%s %s", e.cat.String(), humanBytes(e.bytes))
		txtW := lipgloss.Width(txt)
		add := txtW
		if placed > 0 {
			add += sepW
		}
		// Reserve room for a "+N more" suffix if entries would remain after this.
		reserve := 0
		if leftover := len(entries) - (i + 1); leftover > 0 {
			reserve = sepW + lipgloss.Width(fmt.Sprintf("+%d more", leftover))
		}
		if placed > 0 && rowW+add+reserve > maxW {
			break
		}
		if placed > 0 {
			row += mutedStyle.Render(sep)
			rowW += sepW
		}
		row += categoryStyle(e.cat).Render(txt)
		rowW += txtW
		placed++
	}
	if placed < len(entries) {
		row += mutedStyle.Render(fmt.Sprintf("%s+%d more", sep, len(entries)-placed))
	}
	return row
}

// indentBlock prefixes every line of a multi-line block with pad.
func indentBlock(block, pad string) string {
	lines := strings.Split(block, "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

// proportion returns round(width * num / denom) clamped to >= 0.
func proportion(width int, num, denom int64) int {
	if denom <= 0 || width <= 0 {
		return 0
	}
	return max(int(int64(width)*num/denom), 0)
}
