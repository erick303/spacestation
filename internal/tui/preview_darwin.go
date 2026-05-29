//go:build darwin

package tui

import (
	"io"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
)

// previewSupported reports whether Quick Look previews are available on this
// platform. macOS ships `qlmanage`, so previews are on.
func previewSupported() bool { return true }

// previewCmd launches macOS Quick Look (`qlmanage -p`) on path. qlmanage runs
// in the foreground until the panel is closed, but Bubble Tea runs commands in
// their own goroutine, so the TUI stays interactive behind the floating panel.
// Output is discarded — qlmanage is chatty and we only care about the window.
func previewCmd(path string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("qlmanage", "-p", path)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		_ = cmd.Run() // blocks until the user closes the panel
		return previewClosedMsg{}
	}
}
