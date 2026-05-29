package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/erick303/spacestation/internal/config"
	"github.com/erick303/spacestation/internal/scan"
)

// locationExists reports whether a fixed location is present on disk. The
// Locations editor only lists existing locations, so the list stays relevant to
// the machine. It's a package var so tests can make the list deterministic.
var locationExists = func(expanded string) bool {
	info, err := os.Stat(expanded)
	return err == nil && info.IsDir()
}

// The settings screen serves two roles from one code path: first-run onboarding
// (firstRun == true, reached before any scan) and mid-session config editing
// (reached with `e` from the browse view). The host model decides what happens
// after a save/cancel; this file just edits a draft config and reports the
// outcome.

// settingsOutcome tells the host model whether the settings screen is still
// active, was saved, or was cancelled.
type settingsOutcome int

const (
	settingsNone settingsOutcome = iota
	settingsSaved
	settingsCancelled
)

// settingsField identifies each editable row, in display order.
type settingsField int

const (
	fldLocations settingsField = iota
	fldDownloads
	fldTrash
	fldScreens
	fldSelAge
	fldDlAge
	fldDlSize
	fldShotAge
	numSettingsFields
)

func isBoolField(f settingsField) bool { return f >= fldDownloads && f <= fldScreens }
func isIntField(f settingsField) bool  { return f >= fldSelAge && f <= fldShotAge }

// settingsRows is the label for each field, in display order. Kept as one table
// so the list view and the help-box title agree.
var settingsRows = []struct {
	f     settingsField
	label string
}{
	{fldLocations, "Locations"},
	{fldDownloads, "Include ~/Downloads"},
	{fldTrash, "Include ~/.Trash"},
	{fldScreens, "Include screenshots"},
	{fldSelAge, "Pre-select regenerable items older than (days)"},
	{fldDlAge, "Downloads pre-select min age (days)"},
	{fldDlSize, "Downloads pre-select min size (MB)"},
	{fldShotAge, "Screenshots pre-select min age (days)"},
}

func labelFor(f settingsField) string {
	for _, r := range settingsRows {
		if r.f == f {
			return r.label
		}
	}
	return ""
}

func fieldHelp(f settingsField) string {
	switch f {
	case fldLocations:
		return "Where to look for reclaimable space: known tool caches (Xcode, Docker, Go, Cargo, npm, Homebrew, system caches) plus your project roots, which are walked for build artifacts. Toggle any off, or add your own paths."
	case fldDownloads:
		return "Scan ~/Downloads for old, large files. They're user content, so cleaning moves them to the Trash rather than deleting outright."
	case fldTrash:
		return "List what's already in ~/.Trash so you can review it, remove items permanently, or empty the whole thing."
	case fldScreens:
		return "Scan your screenshot folder for macOS screenshots. User content — cleaning moves them to the Trash."
	case fldSelAge:
		return "Regenerable items (caches, build output) are pre-checked only if untouched for at least this many days. Set 0 to pre-check regardless of age."
	case fldDlAge:
		return "Only pre-check ~/Downloads items at least this many days old. Younger ones are still listed, just unchecked."
	case fldDlSize:
		return "Only pre-check ~/Downloads items at least this many MB. Smaller ones are still listed, just unchecked."
	case fldShotAge:
		return "Only pre-check screenshots at least this many days old."
	}
	return ""
}

const rootHelp = "Project root: walked recursively for build-artifact directories (node_modules, target, dist, .venv, __pycache__, …). Add the folders where your repos live."

// locEntry is one row in the Locations sub-editor. fixed entries are known
// scan targets that can only be toggled; non-fixed entries are project roots,
// which can also be removed.
type locEntry struct {
	path   string
	on     bool
	fixed  bool
	label  string // muted tag shown after the path
	detail string // per-item help text
}

type settingsModel struct {
	firstRun bool
	draft    config.Config
	cursor   settingsField

	// int-field inline editing
	editing bool
	input   textinput.Model

	// Locations sub-editor
	locsOpen   bool
	locs       []locEntry
	locsCursor int // 0..len(locs); len(locs) is the "+ add path" row
	addingLoc  bool

	// help box: when non-empty a bordered box is shown and the next key
	// dismisses it.
	helpText string
}

func newSettings(cfg config.Config, firstRun bool) settingsModel {
	disabled := make(map[string]bool, len(cfg.Scan.DisabledLocations))
	for _, d := range cfg.Scan.DisabledLocations {
		disabled[d] = true
	}
	var locs []locEntry
	for _, l := range scan.DefaultLocations() {
		// Only surface locations that actually exist on this machine.
		if !locationExists(config.Expand(l.Path)) {
			continue
		}
		locs = append(locs, locEntry{
			path:   l.Path,
			on:     !disabled[l.Path],
			fixed:  true,
			label:  l.Label,
			detail: l.Detail,
		})
	}
	for _, r := range cfg.Scan.ProjectRoots {
		locs = append(locs, locEntry{
			path:   r,
			on:     true,
			fixed:  false,
			label:  "project root",
			detail: rootHelp,
		})
	}
	ti := textinput.New()
	ti.Prompt = ""
	return settingsModel{firstRun: firstRun, draft: cfg, locs: locs, input: ti}
}

// effectiveConfig folds the Locations editor back into the draft: enabled
// project roots into ProjectRoots, disabled fixed locations into
// DisabledLocations.
func (s settingsModel) effectiveConfig() config.Config {
	cfg := s.draft

	// Fixed locations shown in the editor, so we can tell apart "the user
	// re-enabled this" from "this was filtered out because it doesn't exist".
	shownFixed := make(map[string]bool)
	for _, l := range s.locs {
		if l.fixed {
			shownFixed[l.path] = true
		}
	}

	var roots, disabled []string
	// Preserve disabled state for locations that weren't shown (absent from
	// disk), so toggling one off isn't silently lost if the cache reappears.
	for _, d := range s.draft.Scan.DisabledLocations {
		if !shownFixed[d] {
			disabled = append(disabled, d)
		}
	}
	for _, l := range s.locs {
		switch {
		case !l.fixed && l.on:
			roots = append(roots, l.path)
		case l.fixed && !l.on:
			disabled = append(disabled, l.path)
		}
	}
	cfg.Scan.ProjectRoots = roots
	cfg.Scan.DisabledLocations = disabled
	return cfg
}

func (s settingsModel) boolVal(f settingsField) bool {
	switch f {
	case fldDownloads:
		return s.draft.Scan.IncludeDownloads
	case fldTrash:
		return s.draft.Scan.IncludeTrash
	case fldScreens:
		return s.draft.Scan.IncludeScreenshots
	}
	return false
}

func (s *settingsModel) toggleBool(f settingsField) {
	switch f {
	case fldDownloads:
		s.draft.Scan.IncludeDownloads = !s.draft.Scan.IncludeDownloads
	case fldTrash:
		s.draft.Scan.IncludeTrash = !s.draft.Scan.IncludeTrash
	case fldScreens:
		s.draft.Scan.IncludeScreenshots = !s.draft.Scan.IncludeScreenshots
	}
}

func (s settingsModel) intVal(f settingsField) int64 {
	switch f {
	case fldSelAge:
		return int64(s.draft.Selection.DefaultSelectMinAgeDays)
	case fldDlAge:
		return int64(s.draft.Selection.DownloadsMinAgeDays)
	case fldDlSize:
		return s.draft.Selection.DownloadsMinSizeMB
	case fldShotAge:
		return int64(s.draft.Selection.ScreenshotsMinAgeDays)
	}
	return 0
}

func (s *settingsModel) setInt(f settingsField, v int64) {
	if v < 0 {
		v = 0
	}
	switch f {
	case fldSelAge:
		s.draft.Selection.DefaultSelectMinAgeDays = int(v)
	case fldDlAge:
		s.draft.Selection.DownloadsMinAgeDays = int(v)
	case fldDlSize:
		s.draft.Selection.DownloadsMinSizeMB = v
	case fldShotAge:
		s.draft.Selection.ScreenshotsMinAgeDays = int(v)
	}
}

func (s settingsModel) update(msg tea.Msg) (settingsModel, tea.Cmd, settingsOutcome) {
	// A help box swallows the next keypress to dismiss itself.
	if s.helpText != "" {
		if _, ok := msg.(tea.KeyMsg); ok {
			s.helpText = ""
		}
		return s, nil, settingsNone
	}

	switch {
	case s.locsOpen:
		return s.updateLocs(msg)
	case s.editing:
		return s.updateEditing(msg)
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil, settingsNone
	}
	switch key.String() {
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "j":
		if s.cursor < numSettingsFields-1 {
			s.cursor++
		}
	case "?":
		s.helpText = fieldHelp(s.cursor)
	case " ":
		if isBoolField(s.cursor) {
			s.toggleBool(s.cursor)
		}
	case "enter":
		switch {
		case s.cursor == fldLocations:
			s.locsOpen = true
			s.locsCursor = 0
		case isBoolField(s.cursor):
			s.toggleBool(s.cursor)
		case isIntField(s.cursor):
			// Show the current value as a placeholder and start empty, so the
			// first keystroke replaces it; committing empty keeps it unchanged.
			s.editing = true
			s.input.SetValue("")
			s.input.Placeholder = strconv.FormatInt(s.intVal(s.cursor), 10)
			s.input.Focus()
			return s, textinput.Blink, settingsNone
		}
	case "s", "ctrl+s":
		return s, nil, settingsSaved
	case "esc", "q", "ctrl+c":
		return s, nil, settingsCancelled
	}
	return s, nil, settingsNone
}

// updateEditing handles the inline numeric editor for int fields.
func (s settingsModel) updateEditing(msg tea.Msg) (settingsModel, tea.Cmd, settingsOutcome) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			// Parse-or-ignore: a non-numeric entry leaves the field unchanged.
			if v, err := strconv.ParseInt(strings.TrimSpace(s.input.Value()), 10, 64); err == nil {
				s.setInt(s.cursor, v)
			}
			s.editing = false
			s.input.Blur()
			return s, nil, settingsNone
		case "esc":
			s.editing = false
			s.input.Blur()
			return s, nil, settingsNone
		}
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return s, cmd, settingsNone
}

// updateLocs handles the Locations sub-editor (toggle, add root, remove root).
func (s settingsModel) updateLocs(msg tea.Msg) (settingsModel, tea.Cmd, settingsOutcome) {
	if s.addingLoc {
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "enter":
				if p := strings.TrimSpace(s.input.Value()); p != "" {
					s.locs = append(s.locs, locEntry{
						path: p, on: true, fixed: false, label: "project root", detail: rootHelp,
					})
				}
				s.addingLoc = false
				s.input.Blur()
				s.input.SetValue("")
				s.locsCursor = len(s.locs) // stay on the add row
				return s, nil, settingsNone
			case "esc":
				s.addingLoc = false
				s.input.Blur()
				s.input.SetValue("")
				return s, nil, settingsNone
			}
		}
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(msg)
		return s, cmd, settingsNone
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil, settingsNone
	}
	addRow := len(s.locs)
	switch key.String() {
	case "up", "k":
		if s.locsCursor > 0 {
			s.locsCursor--
		}
	case "down", "j":
		if s.locsCursor < addRow {
			s.locsCursor++
		}
	case "?":
		if s.locsCursor < addRow {
			s.helpText = s.locs[s.locsCursor].detail
		} else {
			s.helpText = "Add a folder to scan. Custom paths are treated as project roots — walked for build-artifact directories."
		}
	case " ":
		if s.locsCursor < addRow {
			s.locs[s.locsCursor].on = !s.locs[s.locsCursor].on
		}
	case "enter":
		if s.locsCursor == addRow {
			s.addingLoc = true
			s.input.SetValue("")
			s.input.Focus()
			return s, textinput.Blink, settingsNone
		}
		s.locs[s.locsCursor].on = !s.locs[s.locsCursor].on
	case "d", "delete", "backspace":
		// Only project roots can be removed; fixed locations are toggle-only.
		if s.locsCursor < addRow && !s.locs[s.locsCursor].fixed {
			s.locs = append(s.locs[:s.locsCursor], s.locs[s.locsCursor+1:]...)
			if s.locsCursor > len(s.locs) {
				s.locsCursor = len(s.locs)
			}
		}
	case "esc", "q":
		s.locsOpen = false
	}
	return s, nil, settingsNone
}

func (s settingsModel) view(width, height int) string {
	var base string
	if s.locsOpen {
		base = s.viewLocs(width, height)
	} else {
		base = s.viewMain()
	}
	if s.helpText != "" {
		base += "\n\n" + s.helpBox(width)
	}
	return base
}

func (s settingsModel) helpBox(width int) string {
	boxW := width - 6
	if boxW > 76 {
		boxW = 76
	}
	if boxW < 30 {
		boxW = 30
	}
	style := confirmBoxStyle.Width(boxW)
	return style.Render(headerStyle.Render("Help") + "\n\n" + s.helpText + "\n\n" +
		helpStyle.Render("any key to close"))
}

func (s settingsModel) viewMain() string {
	var b strings.Builder
	title := "Settings"
	if s.firstRun {
		title = "Welcome to spacestation — first-run setup"
	}
	b.WriteString(headerStyle.Render(title) + "\n")
	if s.firstRun {
		b.WriteString(mutedStyle.Render("  Set what to scan and how items are pre-selected. Change it later with the e key.") + "\n")
	}
	b.WriteString("\n")

	labelW := 0
	for _, r := range settingsRows {
		if w := lipgloss.Width(r.label); w > labelW {
			labelW = w
		}
	}
	for _, r := range settingsRows {
		cursor := "  "
		if r.f == s.cursor {
			cursor = cursorArrowStyle.Render("▸ ")
		}
		b.WriteString(cursor + padRight(r.label, labelW) + "   " + s.fieldValueStr(r.f) + "\n")
	}

	b.WriteString("\n")
	tail := "↑/↓ move · space toggle · enter edit · ? help · s save · esc cancel"
	if s.firstRun {
		tail = "↑/↓ move · space toggle · enter edit · ? help · s save & scan · esc quit"
	}
	b.WriteString(helpStyle.Render(tail))
	return b.String()
}

func (s settingsModel) fieldValueStr(f settingsField) string {
	switch {
	case f == fldLocations:
		n := 0
		for _, l := range s.locs {
			if l.on {
				n++
			}
		}
		return mutedStyle.Render(fmt.Sprintf("%d enabled  (enter to edit)", n))
	case isBoolField(f):
		if s.boolVal(f) {
			return checkboxOn
		}
		return checkboxOff
	case isIntField(f):
		if s.editing && f == s.cursor {
			return s.input.View()
		}
		return sizeStyle.Render(strconv.FormatInt(s.intVal(f), 10))
	}
	return ""
}

func (s settingsModel) viewLocs(width, height int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("Locations") + "\n")
	b.WriteString(mutedStyle.Render("  Known tool caches (defaults) plus your project roots, which are walked for build artifacts.") + "\n\n")

	total := len(s.locs) + 1 // + the add row
	// Reserve lines for the two-line header, blank, and help line.
	avail := height - 6
	if avail < 6 {
		avail = 6
	}
	start := 0
	if total > avail {
		start = s.locsCursor - avail/2
		if start < 0 {
			start = 0
		}
		if start > total-avail {
			start = total - avail
		}
	}
	end := min(start+avail, total)

	// Align the path column past the widest tag.
	if start > 0 {
		b.WriteString(mutedStyle.Render("  ↑ more") + "\n")
	}
	for i := start; i < end; i++ {
		if i == len(s.locs) { // the add row
			cursor := "  "
			if s.locsCursor == len(s.locs) {
				cursor = cursorArrowStyle.Render("▸ ")
			}
			if s.addingLoc {
				b.WriteString(cursor + "+ " + s.input.View() + "\n")
			} else {
				b.WriteString(cursor + mutedStyle.Render("+ add path…") + "\n")
			}
			continue
		}
		l := s.locs[i]
		cursor := "  "
		if i == s.locsCursor {
			cursor = cursorArrowStyle.Render("▸ ")
		}
		box := checkboxOff
		if l.on {
			box = checkboxOn
		}
		b.WriteString(cursor + box + " " + l.path + "  " + mutedStyle.Render(l.label) + "\n")
	}
	if end < total {
		b.WriteString(mutedStyle.Render("  ↓ more") + "\n")
	}

	b.WriteString("\n" + helpStyle.Render("↑/↓ move · space toggle · enter add/toggle · d remove root · ? help · esc back"))
	return b.String()
}
