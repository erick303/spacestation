package tui

import "github.com/charmbracelet/lipgloss"

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

	groupHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorAccent).
				PaddingLeft(1)

	groupHeaderSelectedStyle = lipgloss.NewStyle().
					Bold(true).
					Foreground(colorBg).
					Background(colorAccent).
					PaddingLeft(1).
					PaddingRight(1)

	itemStyle = lipgloss.NewStyle().PaddingLeft(3)

	itemSelectedStyle = lipgloss.NewStyle().
				Foreground(colorBg).
				Background(colorAccent).
				PaddingLeft(3)

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
