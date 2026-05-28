package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

func TestTruncatePath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"short ASCII unchanged", "/a/b/c", 20, "/a/b/c"},
		{"long ASCII gets ellipsis", "/very/long/path/to/something", 10, "…something"},
		{"max==0 returns empty", "anything", 0, ""},
		// Multi-byte rune (é = 2 bytes, 1 cell). With a byte-slice
		// truncatePath this could land in the middle of the rune and
		// produce invalid UTF-8. Budget=11 content cells + 1 ellipsis = 12.
		{"multibyte rune not cut mid-byte", "/répertoire/de/projet", 12, "…e/de/projet"},
		// Wide rune (CJK = 2 cells per char). Budget=5 content cells.
		// "/项目" = 5 cells fits; "到/项目" = 7 cells doesn't.
		{"wide rune respects display width", "/路径/到/项目", 6, "…/项目"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncatePath(tc.in, tc.max)
			if got != tc.want {
				t.Errorf("truncatePath(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("output is not valid UTF-8: %q", got)
			}
			if tc.max > 0 && lipgloss.Width(got) > tc.max {
				t.Errorf("output width %d exceeds max %d for %q", lipgloss.Width(got), tc.max, got)
			}
		})
	}
}

func TestPadRight_ANSIAware(t *testing.T) {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("red"))
	styled := style.Render("hello")            // 5 visible cells, but raw len ≈ 13+ bytes
	padded := padRight(styled, 10)             // want 10 visible cells total
	if w := lipgloss.Width(padded); w != 10 {
		t.Errorf("padRight ANSI display width = %d, want 10 (raw bytes=%d)", w, len(padded))
	}
	if !strings.HasSuffix(padded, "     ") {
		t.Errorf("padRight didn't append 5 trailing spaces: %q", padded)
	}
}

func TestPadLeft_Multibyte(t *testing.T) {
	// "café" = 4 runes, 4 cells (é = 1 cell), 5 bytes (é = 2 bytes).
	padded := padLeft("café", 6)
	if w := lipgloss.Width(padded); w != 6 {
		t.Errorf("padLeft multibyte display width = %d, want 6", w)
	}
	if !strings.HasPrefix(padded, "  ") {
		t.Errorf("padLeft didn't prepend 2 spaces: %q", padded)
	}
}

func TestPadRight_AlreadyWideEnough(t *testing.T) {
	in := "abcdefgh"
	if got := padRight(in, 5); got != in {
		t.Errorf("padRight on already-wide input mutated it: %q", got)
	}
}
