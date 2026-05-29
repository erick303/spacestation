package tui

import (
	"reflect"
	"slices"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erick303/spacestation/internal/config"
	"github.com/erick303/spacestation/internal/scan"
)

// key builds a tea.KeyMsg whose String() matches the cases in settingsModel.
func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default: // single chars incl. " " render via Runes
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// send feeds a sequence of keys through the model, returning the final state
// and the last non-none outcome seen.
func send(s settingsModel, keys ...string) (settingsModel, settingsOutcome) {
	out := settingsNone
	for _, k := range keys {
		var o settingsOutcome
		s, _, o = s.update(key(k))
		if o != settingsNone {
			out = o
		}
	}
	return s, out
}

// rep repeats a key n times.
func rep(k string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = k
	}
	return out
}

// forceLocations overrides the on-disk existence check so tests that rely on
// fixed locations being shown are deterministic regardless of the host machine.
// It returns a restore func for defer.
func forceLocations(v bool) func() {
	prev := locationExists
	locationExists = func(string) bool { return v }
	return func() { locationExists = prev }
}

func TestSettingsDisableFixedLocation(t *testing.T) {
	defer forceLocations(true)()
	first := scan.DefaultLocations()[0].Path // toggled off below

	s := newSettings(config.Default(), false)
	// cursor on fldLocations: enter opens the editor (cursor on the first
	// fixed location), space unchecks it, esc returns, s saves.
	s, out := send(s, "enter", " ", "esc", "s")
	if out != settingsSaved {
		t.Fatalf("outcome = %v, want settingsSaved", out)
	}
	got := s.effectiveConfig().Scan.DisabledLocations
	if !slices.Contains(got, first) {
		t.Errorf("DisabledLocations = %v, want it to contain %q", got, first)
	}
}

func TestSettingsAddAndToggleRoot(t *testing.T) {
	defer forceLocations(false)() // isolate root logic from host fixed locations
	s := newSettings(config.Config{}, true)

	// Open the editor, jump to the add row (down clamps at it), add a path.
	keys := append([]string{"enter"}, rep("j", 100)...)
	keys = append(keys, "enter", "~", "/", "w", "o", "r", "k", "enter")
	s, _ = send(s, keys...)
	if got := s.effectiveConfig().Scan.ProjectRoots; !reflect.DeepEqual(got, []string{"~/work"}) {
		t.Fatalf("ProjectRoots after add = %v, want [~/work]", got)
	}

	// The new root sits just above the add row; up then space unchecks it.
	s, _ = send(s, "up", " ")
	if got := s.effectiveConfig().Scan.ProjectRoots; len(got) != 0 {
		t.Errorf("ProjectRoots after toggle off = %v, want []", got)
	}
}

func TestSettingsRemoveRootButNotFixed(t *testing.T) {
	defer forceLocations(true)()
	cfg := config.Config{Scan: config.ScanConfig{ProjectRoots: []string{"~/dev"}}}
	s := newSettings(cfg, false)

	// Enter editor, jump to the last entry (the ~/dev root, just above add row),
	// and try to remove it with d.
	keys := append([]string{"enter"}, rep("j", 100)...)
	keys = append(keys, "up", "d") // up off the add row onto ~/dev, then remove
	s, _ = send(s, keys...)
	if got := s.effectiveConfig().Scan.ProjectRoots; len(got) != 0 {
		t.Errorf("ProjectRoots after remove = %v, want []", got)
	}

	// Fixed locations cannot be removed: d on the first one is a no-op.
	s2 := newSettings(config.Default(), false)
	before := len(s2.locs)
	s2, _ = send(s2, "enter", "d")
	if len(s2.locs) != before {
		t.Errorf("fixed location was removed: len %d -> %d", before, len(s2.locs))
	}
}

func TestSettingsEditInt(t *testing.T) {
	s := newSettings(config.Default(), false)
	steps := append(rep("j", int(fldSelAge)), "enter", "4", "5", "enter")
	s, _ = send(s, steps...)
	if got := s.draft.Selection.DefaultSelectMinAgeDays; got != 45 {
		t.Errorf("DefaultSelectMinAgeDays = %d, want 45", got)
	}
}

func TestSettingsHelpDismiss(t *testing.T) {
	s := newSettings(config.Default(), false)
	s, _ = send(s, "?")
	if s.helpText == "" {
		t.Fatal("? did not open a help box")
	}
	s, _ = send(s, "esc") // any key closes it without cancelling
	if s.helpText != "" {
		t.Error("help box did not close on keypress")
	}
}

func TestSettingsCancel(t *testing.T) {
	s := newSettings(config.Default(), false)
	_, out := send(s, "esc")
	if out != settingsCancelled {
		t.Errorf("outcome = %v, want settingsCancelled", out)
	}
}
