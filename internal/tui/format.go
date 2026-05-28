package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// homeRelative shortens absolute paths under $HOME to start with "~/".
// Other paths pass through unchanged.
func homeRelative(p string) string {
	if p == "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

func humanBytes(n int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
		tb = 1024 * gb
	)
	f := float64(n)
	switch {
	case n >= tb:
		return fmt.Sprintf("%.2f TB", f/tb)
	case n >= gb:
		return fmt.Sprintf("%.2f GB", f/gb)
	case n >= mb:
		return fmt.Sprintf("%.1f MB", f/mb)
	case n >= kb:
		return fmt.Sprintf("%.0f KB", f/kb)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// humanCount formats a non-negative integer with thousands separators
// (e.g. 48210 -> "48,210") for readable file counts.
func humanCount(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	// Insert commas from the right.
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func humanAge(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	days := int(d / (24 * time.Hour))
	switch {
	case days <= 0:
		hours := int(d / time.Hour)
		if hours <= 0 {
			return "just now"
		}
		return fmt.Sprintf("%dh ago", hours)
	case days < 30:
		return fmt.Sprintf("%dd ago", days)
	case days < 365:
		return fmt.Sprintf("%dmo ago", days/30)
	default:
		return fmt.Sprintf("%dy ago", days/365)
	}
}

// truncatePath shortens p so that its terminal display width is at most max
// columns, prepending "…" when truncation happened. Operates on rune width
// (so emoji and CJK get charged 2 cells), not bytes — multibyte input is
// never cut mid-rune.
//
// The caller passes display columns, not bytes. Keeping the original
// "tail wins" semantics: the trailing portion of the path is what survives
// truncation, since the suffix (project name, file name) is typically what
// the user wants to read.
func truncatePath(p string, max int) string {
	if max <= 0 {
		return ""
	}
	if runewidth.StringWidth(p) <= max {
		return p
	}
	// Walk runes from the right, accumulating width until we've used max-1
	// cells (leaving room for the leading ellipsis).
	budget := max - 1
	rs := []rune(p)
	cut := len(rs)
	used := 0
	for i := len(rs) - 1; i >= 0; i-- {
		w := runewidth.RuneWidth(rs[i])
		if used+w > budget {
			break
		}
		used += w
		cut = i
	}
	return "…" + string(rs[cut:])
}

// padRight returns s padded on the right with spaces so its terminal display
// width is n. ANSI escape sequences in s are ignored for measurement.
func padRight(s string, n int) string {
	w := lipgloss.Width(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

// padLeft returns s padded on the left with spaces so its terminal display
// width is n. ANSI escape sequences in s are ignored for measurement.
func padLeft(s string, n int) string {
	w := lipgloss.Width(s)
	if w >= n {
		return s
	}
	return strings.Repeat(" ", n-w) + s
}
