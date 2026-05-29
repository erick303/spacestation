//go:build !darwin

package tui

import tea "github.com/charmbracelet/bubbletea"

// previewSupported is false off macOS — Quick Look (`qlmanage`) is macOS-only.
func previewSupported() bool { return false }

// previewCmd is a no-op on non-macOS platforms; the key handler short-circuits
// on !previewSupported() before reaching here.
func previewCmd(string) tea.Cmd { return nil }
