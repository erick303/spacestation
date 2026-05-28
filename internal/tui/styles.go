package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/erick303/spacestation/internal/scan"
)

var (
	colorAccent = lipgloss.AdaptiveColor{Light: "#5B6CFF", Dark: "#7AA2F7"}
	colorMuted  = lipgloss.AdaptiveColor{Light: "#7A7E89", Dark: "#565F89"}
	colorGood   = lipgloss.AdaptiveColor{Light: "#1F9D55", Dark: "#9ECE6A"}
	colorWarn   = lipgloss.AdaptiveColor{Light: "#B97C00", Dark: "#E0AF68"}
	colorDanger = lipgloss.AdaptiveColor{Light: "#C13030", Dark: "#F7768E"}
	// colorSelBg is the subtle band behind the cursor row — just enough lift off
	// the terminal background to trace a row left-to-right, not a loud highlight.
	colorSelBg = lipgloss.AdaptiveColor{Light: "#E6E8F2", Dark: "#2A2F45"}
)

var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent).
			PaddingLeft(1).
			PaddingRight(1)

	statBarStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true).
			PaddingLeft(1)

	statMutedStyle = lipgloss.NewStyle().Foreground(colorMuted).PaddingLeft(2)

	groupHeaderStyle         = lipgloss.NewStyle().Bold(true)
	groupHeaderSelectedStyle = lipgloss.NewStyle().Bold(true) // arrow shows cursor, not bg

	cursorArrowStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	smartTitleStyle  = lipgloss.NewStyle().Bold(true).Foreground(colorWarn)

	checkboxOn  = lipgloss.NewStyle().Foreground(colorGood).Render("[x]")
	checkboxOff = lipgloss.NewStyle().Foreground(colorMuted).Render("[ ]")

	sizeStyle  = lipgloss.NewStyle().Foreground(colorGood).Bold(true)
	ageStyle   = lipgloss.NewStyle().Foreground(colorMuted)
	mutedStyle = lipgloss.NewStyle().Foreground(colorMuted)
	dangerStyle = lipgloss.NewStyle().Foreground(colorDanger).Bold(true)
	warnStyle  = lipgloss.NewStyle().Foreground(colorWarn)

	helpStyle = lipgloss.NewStyle().Foreground(colorMuted).PaddingTop(1).PaddingLeft(1)

	confirmBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorAccent).
			Padding(1, 2)

	scanningStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
)

// Per-category colors, used in the dashboard bar + breakdown line. Hand-picked
// so adjacent segments contrast cleanly in the stacked bar. Indexed by
// scan.Category to mirror the scan-side metadata table — adding a category
// is one new row here in addition to the iota const + categoryMeta entry.
// Empty string means "no specific color, fall back to muted".
var categoryColors = [...]lipgloss.Color{
	scan.CatNodeModules: lipgloss.Color("#9ECE6A"), // green
	scan.CatJSBuild:     lipgloss.Color("#F7768E"), // pink/danger
	scan.CatPython:      lipgloss.Color("#F2CC8F"), // warm yellow
	scan.CatRust:        lipgloss.Color("#FF9E64"), // orange
	scan.CatJVM:         lipgloss.Color("#73DACA"), // teal
	scan.CatGoCache:     lipgloss.Color("#BB9AF7"), // purple
	scan.CatXcode:       lipgloss.Color("#7AA2F7"), // blue/accent
	scan.CatHomebrew:    lipgloss.Color("#C0CAF5"), // foreground
	scan.CatDocker:      lipgloss.Color("#7DCFFF"), // cyan
	scan.CatSystemCache: lipgloss.Color("#E0AF68"), // gold/warn
	scan.CatDownloads:   lipgloss.Color("#A9B1D6"), // light gray
	scan.CatTrash:       lipgloss.Color("#565F89"), // muted
}

func categoryStyle(c scan.Category) lipgloss.Style {
	if int(c) < 0 || int(c) >= len(categoryColors) || categoryColors[c] == "" {
		return lipgloss.NewStyle().Foreground(colorMuted)
	}
	return lipgloss.NewStyle().Foreground(categoryColors[c])
}
