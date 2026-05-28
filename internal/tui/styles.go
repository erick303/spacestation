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
	colorBg     = lipgloss.AdaptiveColor{Light: "#F5F5F7", Dark: "#1A1B26"}
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

	groupHeaderStyle = lipgloss.NewStyle().Bold(true)

	groupHeaderSelectedStyle = lipgloss.NewStyle().
					Bold(true).
					Background(colorAccent).
					PaddingRight(1)

	itemStyle = lipgloss.NewStyle()

	itemSelectedStyle = lipgloss.NewStyle().
				Foreground(colorBg).
				Background(colorAccent)

	smartTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(colorWarn)

	checkboxOn  = lipgloss.NewStyle().Foreground(colorGood).Render("[x]")
	checkboxOff = lipgloss.NewStyle().Foreground(colorMuted).Render("[ ]")

	sizeStyle  = lipgloss.NewStyle().Foreground(colorGood).Bold(true)
	ageStyle   = lipgloss.NewStyle().Foreground(colorMuted)
	pathStyle  = lipgloss.NewStyle()
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
// so adjacent segments contrast cleanly in the stacked bar.
var categoryColors = map[scan.Category]lipgloss.Color{
	scan.CatDocker:      lipgloss.Color("#7DCFFF"), // cyan
	scan.CatNodeModules: lipgloss.Color("#9ECE6A"), // green
	scan.CatSystemCache: lipgloss.Color("#E0AF68"), // gold/warn
	scan.CatJSBuild:     lipgloss.Color("#F7768E"), // pink/danger
	scan.CatGoCache:     lipgloss.Color("#BB9AF7"), // purple
	scan.CatPython:      lipgloss.Color("#F2CC8F"), // warm yellow
	scan.CatHomebrew:    lipgloss.Color("#C0CAF5"), // foreground
	scan.CatRust:        lipgloss.Color("#FF9E64"), // orange
	scan.CatXcode:       lipgloss.Color("#7AA2F7"), // blue/accent
	scan.CatJVM:         lipgloss.Color("#73DACA"), // teal
	scan.CatDownloads:   lipgloss.Color("#A9B1D6"), // light gray
	scan.CatTrash:       lipgloss.Color("#565F89"), // muted
}

func categoryStyle(c scan.Category) lipgloss.Style {
	if col, ok := categoryColors[c]; ok {
		return lipgloss.NewStyle().Foreground(col)
	}
	return lipgloss.NewStyle().Foreground(colorMuted)
}
