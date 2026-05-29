package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/erick303/spacestation/internal/cleanup"
	"github.com/erick303/spacestation/internal/config"
	"github.com/erick303/spacestation/internal/scan"
)

// Public entrypoint.
func Run(cfg config.Config) error {
	m := newModel(cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// Stages of the app.
type stage int

const (
	stageScanning stage = iota
	stageBrowsing
	stageConfirm
	stageCleaning
	stageDone
)

// Row in the rendered list. Either a group header or a candidate.
type row struct {
	isHeader  bool
	cat       scan.Category
	candIdx   int  // index into model.cands when !isHeader
	collapsed bool // for headers
}

type model struct {
	cfg config.Config

	// Configured project roots that don't exist on disk — walkProjects skips
	// them silently, so we surface them in the browse view.
	missingRoots []string

	stage stage

	// scanning state
	spinner       spinner.Model
	scanStart     time.Time
	scanElapsed   time.Duration
	progressMsg   string
	progressFound int
	progressBytes int64
	scanDone      bool

	// scan lifecycle: progressCh is the channel for the *current* scan;
	// scanCancel cancels it; scanFinished is closed by the scan goroutine
	// when it has fully returned (cands sent, size cache saved). A rescan
	// must wait on the old scanFinished before starting the new scan, so
	// that the global size cache isn't being mutated by two scans at once.
	progressCh   chan scan.Progress
	scanCancel   context.CancelFunc
	scanFinished chan struct{}
	cands        []scan.Candidate

	// browsing state
	rows       []row
	cursor     int
	collapsed  map[scan.Category]bool
	width      int
	height     int
	flash      string
	flashUntil time.Time

	// cleaning state
	cleanStart     time.Time
	cleanElapsed   time.Duration
	cleanResults   []cleanup.Result
	cleanedBytes   int64
	cleanCancel    context.CancelFunc
	cleanCancelled bool // set when user hit esc/ctrl+c during stageCleaning

	// trash-removal action (the separate `x` flow, distinct from enter/clean)
	pendingTrash  bool // the pending confirm/clean is an empty/remove-from-Trash op
	trashEmptyAll bool // true = empty the whole Trash; false = remove checked items

	// live progress for the permanent Trash removal. trashProgressCh streams
	// TrashProgress events; trashDone/trashTotal drive the item bar, trashFiles
	// is the running unlink count (so a big item visibly churns), and trashLog
	// holds the last few file/item names so the user sees it run through.
	trashProgressCh chan cleanup.TrashProgress
	trashDone       int
	trashTotal      int
	trashFiles      int
	trashLog        []string

	// "press space again to confirm group toggle" arm state
	armedGroupCat    scan.Category
	armedGroupActive bool
	armedExpiry      time.Time

	// "press c again to confirm clear-all" arm state — c is one key from
	// ctrl+c, so a reflexive c used to nuke the entire selection with no
	// undo. The two-press arm mirrors the group-toggle pattern above.
	armedClearAll       bool
	armedClearAllExpiry time.Time

	// help overlay: toggled by `?` from stageBrowsing. Renders a centered
	// box of all keybindings; closed by `?`, `esc`, or `q`.
	helpVisible bool

	// dashboard
	dashboardOn bool
	diskUsage   scan.DiskUsage
}

// newSpinner builds the scan-progress spinner with the shared style. Used by
// both newModel and resetForRescan so the two stay in sync.
func newSpinner() spinner.Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorAccent)
	return sp
}

func newModel(cfg config.Config) *model {
	return &model{
		cfg:          cfg,
		missingRoots: cfg.MissingRoots(),
		stage:        stageScanning,
		spinner:     newSpinner(),
		scanStart:   time.Now(),
		collapsed:   map[scan.Category]bool{},
		progressCh:  make(chan scan.Progress, 64),
		dashboardOn: true,
	}
}

// resetForRescan returns m to the just-started-a-scan state without
// touching user preferences. cfg, missingRoots, width/height, collapsed
// group state, and dashboardOn all survive a rescan; everything else
// (scan progress, browsing state, cleaning state, armed-toggle state)
// is wiped.
//
// Use this instead of *m = *newModel(...) so future fields on model are
// explicitly classified as "resets" or "persists" rather than zeroed by
// accident.
func (m *model) resetForRescan() {
	m.stage = stageScanning
	m.spinner = newSpinner()
	m.scanStart = time.Now()
	m.scanElapsed = 0
	m.progressMsg = ""
	m.progressFound = 0
	m.progressBytes = 0
	m.scanDone = false

	m.progressCh = make(chan scan.Progress, 64)
	m.scanCancel = nil
	m.scanFinished = nil
	m.cands = nil

	m.rows = nil
	m.cursor = 0
	m.flash = ""
	m.flashUntil = time.Time{}

	m.cleanStart = time.Time{}
	m.cleanElapsed = 0
	m.cleanResults = nil
	m.cleanedBytes = 0
	m.cleanCancel = nil
	m.cleanCancelled = false

	m.pendingTrash = false
	m.trashEmptyAll = false
	m.trashProgressCh = nil
	m.trashDone = 0
	m.trashTotal = 0
	m.trashFiles = 0
	m.trashLog = nil

	m.armedGroupActive = false
	m.armedExpiry = time.Time{}
	m.armedClearAll = false
	m.armedClearAllExpiry = time.Time{}

	m.diskUsage = scan.DiskUsage{}
}

func (m *model) Init() tea.Cmd {
	return m.initWithPrev(nil, nil)
}

// initWithPrev is Init parameterised over an optional previous scan to
// cancel and drain. Used by rescan handlers, which capture the previous
// scan's handles before resetting the model.
func (m *model) initWithPrev(prevCancel context.CancelFunc, prevFinished chan struct{}) tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.beginScan(prevCancel, prevFinished),
		m.pollProgress(),
		tickEvery(),
	)
}

// --- messages ---
// Both scan messages carry the channel they originated from. Update drops
// any message whose channel doesn't match m.progressCh — that way, a
// cancelled scan's last in-flight Progress can't pollute the new scan's UI.

type scanProgressMsg struct {
	ch chan scan.Progress
	p  scan.Progress
}
type scanDoneMsg struct {
	ch      chan scan.Progress
	cands   []scan.Candidate
	elapsed time.Duration
}
type cleanDoneMsg struct {
	results []cleanup.Result
	elapsed time.Duration
	bytes   int64
}
type trashProgressMsg struct {
	ch chan cleanup.TrashProgress
	p  cleanup.TrashProgress
}
type tickMsg time.Time

func tickEvery() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// beginScan starts a new scan. If a previous scan is still alive (prevCancel
// non-nil), it is cancelled and its goroutine is fully drained before the
// new scan starts. The wait happens *inside* the returned Cmd's goroutine
// so the tea event loop never blocks.
func (m *model) beginScan(prevCancel context.CancelFunc, prevFinished chan struct{}) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.scanCancel = cancel
	finished := make(chan struct{})
	m.scanFinished = finished
	ch := m.progressCh
	cfg := m.cfg
	return func() tea.Msg {
		if prevCancel != nil {
			prevCancel()
			if prevFinished != nil {
				<-prevFinished
			}
		}
		start := time.Now()
		cands := scan.Run(ctx, scan.Options{Cfg: cfg}, ch)
		// Persist size-cache so subsequent scans skip walking unchanged trees.
		_ = scan.SaveSizeCache()
		close(finished)
		return scanDoneMsg{ch: ch, cands: cands, elapsed: time.Since(start)}
	}
}

func (m *model) pollProgress() tea.Cmd {
	// Capture the channel ref so the message can identify which scan it
	// came from. A cancelled scan's last emit ends up tagged with the old
	// channel and gets dropped by Update.
	ch := m.progressCh
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return scanProgressMsg{ch: ch, p: p}
	}
}

// pollTrashProgress reads one progress event from the current trash-removal
// channel. Like pollProgress, it tags the message with the channel so a stale
// event can be dropped, and returns nil (stopping the poll loop) once the
// channel is closed by the removal goroutine.
func (m *model) pollTrashProgress() tea.Cmd {
	ch := m.trashProgressCh
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return trashProgressMsg{ch: ch, p: p}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tickMsg:
		// keep scan elapsed counter ticking while scanning
		if m.stage == stageScanning {
			m.scanElapsed = time.Since(m.scanStart)
		}
		return m, tickEvery()

	case scanProgressMsg:
		if msg.ch != m.progressCh {
			return m, nil // stale: from a cancelled scan
		}
		m.progressMsg = msg.p.Message
		if msg.p.Found > 0 {
			m.progressFound = msg.p.Found
			m.progressBytes = msg.p.Bytes
		}
		return m, m.pollProgress()

	case scanDoneMsg:
		if msg.ch != m.progressCh {
			return m, nil // stale: from a cancelled scan
		}
		m.cands = msg.cands
		m.scanElapsed = msg.elapsed
		m.scanDone = true
		m.diskUsage = scan.GetDiskUsage("/")
		m.rebuildRows()
		m.stage = stageBrowsing
		return m, nil

	case trashProgressMsg:
		if msg.ch != m.trashProgressCh {
			return m, nil // stale: from a previous removal
		}
		// Counters come as atomic snapshots from concurrent workers, so events
		// can arrive slightly out of order; take the max to stay monotonic and
		// avoid the bar/counter flickering backwards.
		m.trashDone = max(m.trashDone, msg.p.Done)
		m.trashFiles = max(m.trashFiles, msg.p.Files)
		if msg.p.Total > 0 {
			m.trashTotal = msg.p.Total // authoritative count from ReadDir
		}
		// Rolling log of the last 3 names, newest last.
		name := filepath.Base(msg.p.Path)
		if msg.p.Err != nil {
			name += " (failed)"
		}
		m.trashLog = append(m.trashLog, name)
		if len(m.trashLog) > 3 {
			m.trashLog = m.trashLog[len(m.trashLog)-3:]
		}
		return m, m.pollTrashProgress()

	case cleanDoneMsg:
		m.cleanResults = msg.results
		m.cleanedBytes = msg.bytes
		m.cleanElapsed = msg.elapsed
		m.stage = stageDone
		return m, nil

	case previewClosedMsg:
		// The Quick Look panel closed; it owns its own window, nothing to do.
		return m, nil
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.stage {
	case stageScanning:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			// Cancel the in-flight scan so its goroutines can unwind
			// before the process exits — frees FDs and lets the
			// size-cache write that fires at the tail of scan.Run
			// finish cleanly.
			if m.scanCancel != nil {
				m.scanCancel()
			}
			return m, tea.Quit
		}
		return m, nil

	case stageBrowsing:
		return m.handleBrowseKey(msg)

	case stageConfirm:
		switch msg.String() {
		case "y", "Y", "enter":
			m.stage = stageCleaning
			m.cleanStart = time.Now()
			if m.pendingTrash {
				// Stream per-item progress alongside the removal so the
				// cleaning screen isn't a silent spinner on big Trashes.
				return m, tea.Batch(m.executeTrashClean(), m.pollTrashProgress())
			}
			return m, m.executeClean()
		case "n", "N", "esc", "q":
			m.stage = stageBrowsing
			return m, nil
		}
		return m, nil

	case stageCleaning:
		switch msg.String() {
		case "esc", "ctrl+c":
			// Cancel any in-flight subprocess (via exec.CommandContext) and
			// short-circuit the command loop. The cleanup goroutine returns
			// cleanDoneMsg with whatever results accumulated; Update then
			// transitions to stageDone so the user sees partial results.
			if m.cleanCancel != nil && !m.cleanCancelled {
				m.cleanCancel()
				m.cleanCancelled = true
			}
			return m, nil
		case "q":
			// Same cancel, but also tear down the program. cleanup.Execute
			// will return shortly; if the process has already exited by
			// then, the goroutine dies with it.
			if m.cleanCancel != nil {
				m.cleanCancel()
			}
			return m, tea.Quit
		}
		return m, nil

	case stageDone:
		switch msg.String() {
		case "q", "enter", "esc", "ctrl+c":
			return m, tea.Quit
		case "r":
			// In stageDone the previous scan has fully returned (we
			// reached this stage via scanDoneMsg → cleanDoneMsg).
			// scanFinished is already closed so the new scan won't
			// actually block, but we pass it through for symmetry.
			prevCancel := m.scanCancel
			prevFinished := m.scanFinished
			m.resetForRescan()
			return m, m.initWithPrev(prevCancel, prevFinished)
		}
		return m, nil
	}
	return m, nil
}

func (m *model) handleBrowseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Help overlay intercepts every key — close on ?, esc, or q; swallow
	// everything else so a stray space doesn't toggle items behind the modal.
	if m.helpVisible {
		switch msg.String() {
		case "?", "esc", "q":
			m.helpVisible = false
		}
		return m, nil
	}
	switch msg.String() {
	case "?":
		m.helpVisible = true
		return m, nil
	case "q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
		m.armedGroupActive = false
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		m.armedGroupActive = false
	case "g", "home":
		m.cursor = 0
		m.armedGroupActive = false
	case "G", "end":
		m.cursor = len(m.rows) - 1
		m.armedGroupActive = false
	case "]", "}":
		m.jumpToGroup(+1)
		m.armedGroupActive = false
	case "[", "{":
		m.jumpToGroup(-1)
		m.armedGroupActive = false
	case "pgdown", " ":
		// space toggles current item
		if msg.String() == " " {
			m.toggleCurrent()
		} else {
			m.cursor = min(len(m.rows)-1, m.cursor+10)
		}
	case "pgup":
		m.cursor = max(0, m.cursor-10)
	case "a":
		m.selectGroupAtCursor(true)
	case "u":
		m.selectGroupAtCursor(false)
	case "A":
		m.selectAll(true)
	case "c":
		// Two-press confirmation: c is one key away from ctrl+c, so a
		// reflexive c used to silently nuke the entire selection. First
		// press arms; second press within 3s commits.
		armed := m.armedClearAll && time.Now().Before(m.armedClearAllExpiry)
		if !armed {
			n := m.countSelected()
			if n == 0 {
				// Nothing to clear — no point arming.
				m.setFlash("Nothing selected to clear.")
				break
			}
			m.armedClearAll = true
			m.armedClearAllExpiry = time.Now().Add(3 * time.Second)
			m.setFlash(fmt.Sprintf("Press c again to clear all %d selections", n))
			break
		}
		m.armedClearAll = false
		m.selectAll(false)
	case "tab":
		m.toggleCollapseAtCursor()
	case "enter":
		// Enter always means "clean" (move to Trash). Trash items are excluded —
		// they have their own `x` action. Tab is for collapse.
		if m.countSelectedCleanable() > 0 {
			m.pendingTrash = false
			m.stage = stageConfirm
		} else {
			m.setFlash("No items selected. Press space to select, A for all, then enter.")
		}
	case "x":
		// Separate, permanent Trash action — never mixed with the move-to-Trash
		// clean. Acts on checked Trash items if any, else empties the whole Trash.
		selCount, trashCount := 0, 0
		for _, c := range m.cands {
			if c.Category != scan.CatTrash {
				continue
			}
			trashCount++
			if c.Selected {
				selCount++
			}
		}
		if trashCount == 0 {
			m.setFlash("Trash is empty.")
			return m, nil
		}
		m.pendingTrash = true
		m.trashEmptyAll = selCount == 0
		m.stage = stageConfirm
	case "r":
		// Rescan from browse stage. The previous scan goroutine is
		// already done (we're past scanDoneMsg) so prevCancel/prevFinished
		// are no-ops, but threading them through keeps the lifecycle
		// invariant explicit.
		prevCancel := m.scanCancel
		prevFinished := m.scanFinished
		m.resetForRescan()
		return m, m.initWithPrev(prevCancel, prevFinished)
	case "v":
		m.dashboardOn = !m.dashboardOn
	case "p":
		return m.previewAtCursor()
	}
	return m, nil
}

// previewAtCursor opens the file under the cursor in macOS Quick Look, or
// flashes a reason when it can't (header row, folder, cleanup-command row,
// missing file, unsupported type, or non-macOS build).
func (m *model) previewAtCursor() (tea.Model, tea.Cmd) {
	if !previewSupported() {
		m.setFlash("Preview (Quick Look) is only available on macOS.")
		return m, nil
	}
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return m, nil
	}
	r := m.rows[m.cursor]
	if r.isHeader {
		m.setFlash("Nothing to preview here.")
		return m, nil
	}
	cand := m.cands[r.candIdx]
	if msg, rejected := previewRejection(cand); rejected {
		m.setFlash(msg)
		return m, nil
	}
	info, err := os.Stat(cand.Path)
	switch {
	case err != nil:
		m.setFlash("File no longer exists — rescan with r.")
		return m, nil
	case info.IsDir():
		m.setFlash("Can't preview a folder.")
		return m, nil
	}
	return m, previewCmd(cand.Path)
}

func (m *model) toggleCurrent() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	r := m.rows[m.cursor]
	if r.isHeader {
		// Two-press confirmation: a stray space on a group header used to
		// silently flip every item in it. Now the first press arms the toggle
		// and flashes a hint; a second press within 3s commits.
		armed := m.armedGroupActive && m.armedGroupCat == r.cat && time.Now().Before(m.armedExpiry)
		if !armed {
			count, _ := m.groupStats(r.cat)
			m.armedGroupCat = r.cat
			m.armedGroupActive = true
			m.armedExpiry = time.Now().Add(3 * time.Second)
			m.setFlash(fmt.Sprintf("Press space again to toggle all %d items in %s (a/u also work)", count, r.cat.String()))
			return
		}
		// Confirmed — do the toggle, disarm.
		m.armedGroupActive = false
		all := true
		for i := range m.cands {
			if m.cands[i].Category == r.cat && !m.cands[i].Selected {
				all = false
				break
			}
		}
		for i := range m.cands {
			if m.cands[i].Category == r.cat {
				m.cands[i].Selected = !all
			}
		}
		return
	}
	// Any non-header space disarms group toggle.
	m.armedGroupActive = false
	m.cands[r.candIdx].Selected = !m.cands[r.candIdx].Selected
}

func (m *model) selectGroupAtCursor(sel bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	cat := m.rows[m.cursor].cat
	for i := range m.cands {
		if m.cands[i].Category == cat {
			m.cands[i].Selected = sel
		}
	}
}

func (m *model) selectAll(sel bool) {
	for i := range m.cands {
		m.cands[i].Selected = sel
	}
}

// jumpToGroup moves the cursor to the next/previous header row.
// dir = +1 or -1. Wraps at the ends of the list.
func (m *model) jumpToGroup(dir int) {
	if len(m.rows) == 0 {
		return
	}
	n := len(m.rows)
	for i := 1; i <= n; i++ {
		idx := (m.cursor + dir*i + n) % n
		if m.rows[idx].isHeader {
			m.cursor = idx
			return
		}
	}
}

func (m *model) toggleCollapseAtCursor() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	cat := m.rows[m.cursor].cat
	m.collapsed[cat] = !m.collapsed[cat]
	m.rebuildRows()
	// Land cursor on the (possibly now-collapsed) group's header — keeps the
	// user oriented when they collapse while sitting on an inner item.
	for i, row := range m.rows {
		if row.isHeader && row.cat == cat {
			m.cursor = i
			break
		}
	}
}

func (m *model) rebuildRows() {
	// group candidates by category, sorted by Category.SortOrder
	groups := map[scan.Category][]int{}
	for i, c := range m.cands {
		groups[c.Category] = append(groups[c.Category], i)
	}
	cats := make([]scan.Category, 0, len(groups))
	for c := range groups {
		cats = append(cats, c)
	}
	sort.Slice(cats, func(i, j int) bool { return cats[i].SortOrder() < cats[j].SortOrder() })

	rows := make([]row, 0, len(m.cands)+len(cats))
	for _, cat := range cats {
		rows = append(rows, row{isHeader: true, cat: cat, collapsed: m.collapsed[cat]})
		if m.collapsed[cat] {
			continue
		}
		indices := groups[cat]
		sort.Slice(indices, func(i, j int) bool {
			return m.cands[indices[i]].SizeBytes > m.cands[indices[j]].SizeBytes
		})
		for _, idx := range indices {
			rows = append(rows, row{isHeader: false, cat: cat, candIdx: idx})
		}
	}
	m.rows = rows
	if m.cursor >= len(rows) {
		m.cursor = max(0, len(rows)-1)
	}
}

// isCleanable reports whether c belongs to the move-to-Trash set: selected and
// not itself a Trash item (Trash has its own `x` empty action).
func isCleanable(c scan.Candidate) bool {
	return c.Selected && c.Category != scan.CatTrash
}

// statsWhere returns the count and total bytes of candidates matching pred.
// It's the single pass behind every count/bytes/group helper below.
func (m *model) statsWhere(pred func(scan.Candidate) bool) (count int, bytes int64) {
	for _, c := range m.cands {
		if pred(c) {
			count++
			bytes += c.SizeBytes
		}
	}
	return
}

func (m *model) countSelected() int {
	n, _ := m.statsWhere(func(c scan.Candidate) bool { return c.Selected })
	return n
}

// countSelectedCleanable counts selected items eligible for the move-to-Trash
// clean — i.e. everything except Trash items, which have their own `x` action.
func (m *model) countSelectedCleanable() int {
	n, _ := m.statsWhere(isCleanable)
	return n
}

func (m *model) selectedBytes() int64 {
	_, b := m.statsWhere(func(c scan.Candidate) bool { return c.Selected })
	return b
}

// trashTargetStats counts the Trash items the pending `x` action will remove:
// all Trash items when trashEmptyAll, else the checked ones.
func (m *model) trashTargetStats() (count int, bytes int64) {
	return m.statsWhere(func(c scan.Candidate) bool {
		return c.Category == scan.CatTrash && (m.trashEmptyAll || c.Selected)
	})
}

// cleanableBytes is selectedBytes excluding Trash items (the move-to-Trash set).
func (m *model) cleanableBytes() int64 {
	_, b := m.statsWhere(isCleanable)
	return b
}

func (m *model) totalBytes() int64 {
	_, b := m.statsWhere(func(scan.Candidate) bool { return true })
	return b
}

func (m *model) groupStats(cat scan.Category) (count int, bytes int64) {
	return m.statsWhere(func(c scan.Candidate) bool { return c.Category == cat })
}

func (m *model) groupSelectedStats(cat scan.Category) (count int, bytes int64) {
	return m.statsWhere(func(c scan.Candidate) bool { return c.Category == cat && c.Selected })
}

func (m *model) setFlash(s string) {
	m.flash = s
	m.flashUntil = time.Now().Add(3 * time.Second)
}

func (m *model) executeClean() tea.Cmd {
	var selected []scan.Candidate
	var bytes int64
	for _, c := range m.cands {
		// Trash items are excluded — they're handled by executeTrashClean.
		if isCleanable(c) {
			selected = append(selected, c)
			bytes += c.SizeBytes
		}
	}
	// Build the cancel context synchronously so the key handler can call
	// m.cleanCancel() as soon as the cleaning stage starts.
	ctx, cancel := context.WithCancel(context.Background())
	m.cleanCancel = cancel
	cfg := m.cfg
	return func() tea.Msg {
		defer cancel()
		start := time.Now()
		results := cleanup.Execute(ctx, selected)
		// Invalidate size-cache entries for what we successfully removed so the
		// next scan re-measures them.
		for _, r := range results {
			if r.Err == nil && r.Candidate.Action == scan.ActionDelete {
				scan.InvalidateSizeCache(r.Candidate.Path)
			}
		}
		_ = scan.SaveSizeCache()
		_ = cfg // capture so closure doesn't reference m
		return cleanDoneMsg{results: results, elapsed: time.Since(start), bytes: bytes}
	}
}

// executeTrashClean permanently removes Trash items — the separate `x` action.
// When trashEmptyAll is set it empties the entire Trash (including hidden
// entries); otherwise it removes just the checked Trash items.
func (m *model) executeTrashClean() tea.Cmd {
	var targets []scan.Candidate
	var bytes int64
	for _, c := range m.cands {
		if c.Category != scan.CatTrash {
			continue
		}
		if m.trashEmptyAll || c.Selected {
			targets = append(targets, c)
			bytes += c.SizeBytes
		}
	}
	emptyAll := m.trashEmptyAll
	trashDir := config.Expand("~/.Trash")
	ctx, cancel := context.WithCancel(context.Background())
	m.cleanCancel = cancel

	// Set up live progress. Seed trashTotal from the scanned count so the bar
	// has a denominator immediately; cleanup will correct it from the real
	// ReadDir count (which also covers hidden Trash entries) on the first event.
	ch := make(chan cleanup.TrashProgress, 256)
	m.trashProgressCh = ch
	m.trashDone = 0
	m.trashFiles = 0
	m.trashLog = nil
	if seed, _ := m.trashTargetStats(); seed > 0 {
		m.trashTotal = seed
	}
	// Non-blocking so parallel removal workers are never throttled by the UI.
	// Dropping an event is harmless: each event carries the running Done count,
	// so the count stays accurate; we only ever lose an intermediate log line.
	progress := func(p cleanup.TrashProgress) {
		select {
		case ch <- p:
		default:
		}
	}

	return func() tea.Msg {
		defer cancel()
		defer close(ch)
		start := time.Now()
		var results []cleanup.Result
		if emptyAll {
			err := cleanup.EmptyTrash(ctx, trashDir, progress)
			results = []cleanup.Result{{
				Candidate: scan.Candidate{
					Path:     trashDir,
					Title:    "Trash",
					Category: scan.CatTrash,
					Action:   scan.ActionDelete,
				},
				Err: err,
			}}
		} else {
			results = cleanup.RemoveFromTrash(ctx, targets, progress)
		}
		for _, r := range results {
			if r.Err == nil {
				scan.InvalidateSizeCache(r.Candidate.Path)
			}
		}
		_ = scan.SaveSizeCache()
		return cleanDoneMsg{results: results, elapsed: time.Since(start), bytes: bytes}
	}
}

// --- View ---

func (m *model) View() string {
	switch m.stage {
	case stageScanning:
		return m.viewScanning()
	case stageBrowsing:
		if m.helpVisible {
			return m.viewBrowsing() + "\n" + m.viewHelpOverlay()
		}
		return m.viewBrowsing()
	case stageConfirm:
		return m.viewBrowsing() + "\n" + m.viewConfirm()
	case stageCleaning:
		return m.viewCleaning()
	case stageDone:
		return m.viewDone()
	}
	return ""
}

// viewHelpOverlay renders the full keybindings list as a bordered box,
// mirroring the confirmBoxStyle used for the delete-confirm modal.
// Triggered by `?` from stageBrowsing; closed by `?`, `esc`, or `q`.
func (m *model) viewHelpOverlay() string {
	rows := [][2]string{
		{"↑/↓, j/k", "move cursor"},
		{"g / G", "jump to top / bottom (also home / end)"},
		{"[ / ]", "jump to previous / next group header"},
		{"pgup / pgdn", "page up / down"},
		{"space", "toggle current item (or group on a header — two-press)"},
		{"a / u", "select / unselect all items in current group"},
		{"A", "select all"},
		{"c", "clear all (two-press to confirm)"},
		{"tab", "collapse / expand group at cursor"},
		{"enter", "open confirmation, then clean (move to Trash)"},
		{"x", "permanent Trash action (checked items, or empty all)"},
		{"p", "preview file at cursor (Quick Look)"},
		{"v", "toggle disk-usage dashboard"},
		{"r", "rescan"},
		{"?", "show / hide this help"},
		{"q / ctrl+c", "quit"},
	}
	// Right-pad the key column to a fixed width so the action column lines up.
	keyW := 0
	for _, r := range rows {
		if w := lipgloss.Width(r[0]); w > keyW {
			keyW = w
		}
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render("Keys") + "\n\n")
	for _, r := range rows {
		b.WriteString("  ")
		b.WriteString(padRight(r[0], keyW))
		b.WriteString("   ")
		b.WriteString(mutedStyle.Render(r[1]))
		b.WriteString("\n")
	}
	b.WriteString("\n" + helpStyle.Render("? / esc / q  close"))
	return confirmBoxStyle.Render(b.String())
}

func (m *model) viewScanning() string {
	title := headerStyle.Render("spacestation")
	line := fmt.Sprintf("%s  %s  scanning… %s", title, m.spinner.View(), scanningStyle.Render(m.progressMsg))
	stats := statMutedStyle.Render(fmt.Sprintf(
		"  found %d candidates, %s reclaimable so far  (%s)",
		m.progressFound, humanBytes(m.progressBytes), m.scanElapsed.Truncate(100*time.Millisecond),
	))
	hint := helpStyle.Render("press q to cancel")
	return "\n" + line + "\n\n" + stats + "\n\n" + hint
}

func (m *model) viewBrowsing() string {
	width := m.width
	if width <= 0 {
		width = 100
	}

	title := headerStyle.Render("spacestation")
	statsLine := statBarStyle.Render(fmt.Sprintf(
		"%s reclaimable", humanBytes(m.totalBytes())))
	selLine := statMutedStyle.Render(fmt.Sprintf(
		"%d of %d selected (%s)",
		m.countSelected(), len(m.cands), humanBytes(m.selectedBytes())))
	scanLine := statMutedStyle.Render(fmt.Sprintf("scanned in %s", m.scanElapsed.Truncate(100*time.Millisecond)))

	divider := mutedStyle.Render("│")
	header := "  " + lipgloss.JoinHorizontal(lipgloss.Top,
		title, "  ", divider, "  ", statsLine, "  ", divider, "  ", selLine, "  ", divider, "  ", scanLine)

	// Two compact help lines so they never wrap unpredictably on narrow terms.
	helpLine1 := "space toggle  a select-group  u clear-group  A select-all  c clear"
	helpLine2 := "tab collapse  [ / ] prev/next group  p preview  v dashboard  enter clean  x empty/remove trash  r rescan  ? help  q quit"
	help := helpStyle.Render(helpLine1 + "\n" + helpLine2)

	flashLine := ""
	if m.flash != "" && time.Now().Before(m.flashUntil) {
		flashLine = warnStyle.Render("  ⚠ " + m.flash)
	}

	// Persistent notice: configured project roots that don't exist were
	// skipped, so the user knows the scan didn't cover what they expected.
	noticeLine := ""
	if len(m.missingRoots) > 0 {
		noticeLine = warnStyle.Render("  ⚠ project roots not found (skipped): " +
			strings.Join(m.missingRoots, ", ") + " — set project_roots in the file shown by `spacestation --config`")
	}

	// detail pane has 3 lines (action, detail, safety+reason)
	detail := m.renderDetail()

	dashboard := ""
	dashboardLines := 0
	if m.dashboardOn {
		dashboard = renderDashboard(width, m.diskUsage, m.cands)
		// Count actual lines — the dashboard's height varies (cards, bars, blanks).
		dashboardLines = strings.Count(dashboard, "\n") + 2 // self + blank below
	}

	// Account for everything that's NOT the list:
	//   header(1) + blank(1) [+ dashboard(N) + blank(1)]
	// + blank-before-detail(1) + detail(3)
	// + blank-before-help(1) + helpStyle.PaddingTop(1) + help(2 lines)
	// + flash(1 when present)
	reserved := 11 + dashboardLines
	if flashLine != "" {
		reserved += 1
	}
	if noticeLine != "" {
		reserved += 1
	}
	viewportHeight := max(m.height-reserved, 5)
	listView := m.renderList(width, viewportHeight)

	parts := []string{header, ""}
	if dashboard != "" {
		parts = append(parts, dashboard, "")
	}
	parts = append(parts, listView, detail)
	if noticeLine != "" {
		parts = append(parts, noticeLine)
	}
	if flashLine != "" {
		parts = append(parts, flashLine)
	}
	parts = append(parts, help)
	return strings.Join(parts, "\n")
}

func (m *model) renderList(width, height int) string {
	if len(m.rows) == 0 {
		if m.scanDone {
			return mutedStyle.Render("\n  Nothing reclaimable found. Great hygiene!")
		}
		return ""
	}

	// Build every visual line up front — including the blank separator before
	// each group — and record which line carries the cursor. Windowing then
	// operates on lines, not rows, so the height budget and the cursor position
	// can't disagree. (The old row-based window let group-separator blanks eat
	// the budget before the loop reached the cursor near the bottom, dropping
	// the cursor row off-screen behind the footer.)
	var lines []string
	cursorLine := 0
	for i, r := range m.rows {
		if i > 0 && r.isHeader {
			lines = append(lines, "") // blank between groups
		}
		if i == m.cursor {
			cursorLine = len(lines)
		}
		if r.isHeader {
			lines = append(lines, m.renderHeaderRow(r, i == m.cursor))
		} else {
			lines = append(lines, m.renderItemRow(width, m.cands[r.candIdx], i == m.cursor))
		}
	}

	if len(lines) <= height {
		return strings.Join(lines, "\n") + "\n"
	}

	// Center the cursor when possible, clamped to the ends.
	start := min(max(cursorLine-height/2, 0), len(lines)-height)
	// Don't open the window on a group-separator blank — it just wastes a line.
	if start > 0 && lines[start] == "" {
		start++
	}
	end := min(start+height, len(lines))
	return strings.Join(lines[start:end], "\n") + "\n"
}

func (m *model) renderHeaderRow(r row, isCursor bool) string {
	count, bytes := m.groupStats(r.cat)
	selCount, selBytes := m.groupSelectedStats(r.cat)
	caret := "▼"
	if m.collapsed[r.cat] {
		caret = "▶"
	}
	var selPart string
	if selCount > 0 {
		selPart = sizeStyle.Render(fmt.Sprintf("· %s cleaning (%d/%d items)", humanBytes(selBytes), selCount, count)) +
			mutedStyle.Render(fmt.Sprintf(" of %s", humanBytes(bytes)))
	} else {
		selPart = mutedStyle.Render(fmt.Sprintf("· %d items · %s", count, humanBytes(bytes)))
	}
	// Category color on the name itself ties the row back to the dashboard mix-bar segment.
	name := categoryStyle(r.cat).Bold(true).Render(r.cat.String())
	body := fmt.Sprintf("%s %s  %s", caret, name, selPart)
	indent := "  "
	if isCursor {
		indent = cursorArrowStyle.Render("▸ ")
		return indent + groupHeaderSelectedStyle.Render(body)
	}
	return indent + groupHeaderStyle.Render(body)
}

func (m *model) renderItemRow(width int, c scan.Candidate, isCursor bool) string {
	pathW := max(width-38, 20)
	label := c.DisplayTitle()
	if c.Action == scan.ActionDelete {
		label = homeRelative(label)
	}
	tag := ""
	if c.Action == scan.ActionCommand {
		tag = "⚡ "
	}
	visText := truncatePath(tag+label, pathW)

	sizeText := padLeft(humanBytes(c.SizeBytes), 9)
	ageText := padLeft(humanAge(c.LastTouched), 9)

	if isCursor {
		return m.renderItemRowCursor(width, pathW, c, visText, sizeText, ageText)
	}

	cb := checkboxOff
	if c.Selected {
		cb = checkboxOn
	}
	// The dot stays its own color — rendered outside any row style.
	dot := categoryStyle(c.Category).Render("●")
	visible := visText
	if c.Action == scan.ActionCommand {
		visible = smartTitleStyle.Render(visible)
	}
	// padRight measures via lipgloss.Width so the ANSI escapes in the styled
	// prefix don't throw the alignment off.
	paddedPath := padRight(visible, pathW)
	inner := fmt.Sprintf("%s %s  %s   %s",
		cb,
		paddedPath,
		sizeStyle.Render(sizeText),
		ageStyle.Render(ageText),
	)
	return "  " + dot + "  " + inner
}

// renderItemRowCursor renders the highlighted cursor row as a continuous band.
// lipgloss (v1.1) drops the background at every inner color reset, so each
// segment AND separator is rendered through a background-carrying style with no
// bare characters between them — every reset is immediately followed by the
// next segment re-asserting the band, leaving no gaps. The trailing pad extends
// the band to the full terminal width so the eye can trace a row across to the
// size/age columns.
func (m *model) renderItemRowCursor(width, pathW int, c scan.Candidate, visText, sizeText, ageText string) string {
	bg := lipgloss.NewStyle().Background(colorSelBg)
	band := func(st lipgloss.Style, s string) string { return st.Background(colorSelBg).Render(s) }

	cbStyle := lipgloss.NewStyle().Foreground(colorMuted).Bold(true)
	cbText := "[ ]"
	if c.Selected {
		cbStyle = lipgloss.NewStyle().Foreground(colorGood).Bold(true)
		cbText = "[x]"
	}

	pathStyle := lipgloss.NewStyle().Bold(true)
	if c.Action == scan.ActionCommand {
		pathStyle = pathStyle.Foreground(colorWarn)
	}
	pathPad := max(pathW-lipgloss.Width(visText), 0)

	// Order mirrors the non-cursor row: ▸ · dot · "  " · checkbox · " " · path · "  " · size · "   " · age.
	var b strings.Builder
	b.WriteString(band(cursorArrowStyle, "▸ "))
	b.WriteString(band(categoryStyle(c.Category), "●"))
	b.WriteString(bg.Render("  "))
	b.WriteString(band(cbStyle, cbText))
	b.WriteString(bg.Render(" "))
	b.WriteString(band(pathStyle, visText))
	b.WriteString(bg.Render(strings.Repeat(" ", pathPad)))
	b.WriteString(bg.Render("  "))
	b.WriteString(band(sizeStyle, sizeText))
	b.WriteString(bg.Render("   "))
	b.WriteString(band(ageStyle.Bold(true), ageText))

	used := 2 + 1 + 2 + 3 + 1 + pathW + 2 + 9 + 3 + 9
	b.WriteString(bg.Render(strings.Repeat(" ", max(width-used, 0))))
	return b.String()
}

func (m *model) renderDetail() string {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return ""
	}
	r := m.rows[m.cursor]
	if r.isHeader {
		hint := r.cat.String() + " group — press tab to collapse, a to select all in group"
		if r.cat == scan.CatTrash {
			hint += " · x to empty/remove (permanent)"
		}
		return mutedStyle.Render("  " + hint)
	}
	c := m.cands[r.candIdx]
	if c.Category == scan.CatTrash {
		actionLine := mutedStyle.Render("  remove (permanent)  "+c.Path) + "\n"
		line2 := mutedStyle.Render("  " + c.Detail)
		line3 := "  " + warnStyle.Render("already in Trash") + mutedStyle.Render("  •  press x to empty Trash or remove checked items")
		return actionLine + line2 + "\n" + line3
	}
	safetyTag := ""
	switch c.Safety {
	case scan.SafetyRegenerable:
		safetyTag = sizeStyle.Render("regenerable")
	case scan.SafetyUserContent:
		safetyTag = warnStyle.Render("user content — review carefully")
	default:
		safetyTag = warnStyle.Render("unknown safety")
	}
	actionLine := ""
	if c.Action == scan.ActionCommand {
		actionLine = "  " + scanningStyle.Render("⚡ "+strings.Join(c.Command, " ")) + "\n"
	} else {
		actionLine = mutedStyle.Render("  delete  "+c.Path) + "\n"
	}
	line2 := mutedStyle.Render("  " + c.Detail)
	line3 := "  " + safetyTag + mutedStyle.Render("  •  ") + mutedStyle.Render(c.Reason)
	return actionLine + line2 + "\n" + line3
}

func (m *model) viewConfirm() string {
	if m.pendingTrash {
		return m.viewConfirmTrash()
	}
	verb := "Move to Trash"
	hint := "Items will go to ~/.Trash — you can restore from Finder."
	count := m.countSelectedCleanable()
	body := fmt.Sprintf(
		"%s  %s\n\n%d items, %s\n\n%s\n\n%s",
		verb,
		statBarStyle.Render(humanBytes(m.cleanableBytes())),
		count,
		humanBytes(m.cleanableBytes()),
		mutedStyle.Render(hint),
		helpStyle.Render("y / enter  confirm     n / esc  cancel"),
	)
	return confirmBoxStyle.Render(body)
}

// viewConfirmTrash renders the confirm box for the separate, permanent Trash
// action (empty-all or remove-checked).
func (m *model) viewConfirmTrash() string {
	count, bytes := m.trashTargetStats()
	verb := dangerStyle.Render("PERMANENTLY EMPTY TRASH")
	if !m.trashEmptyAll {
		verb = dangerStyle.Render("PERMANENTLY REMOVE FROM TRASH")
	}
	body := fmt.Sprintf(
		"%s  %s\n\n%d items, %s\n\n%s\n\n%s",
		verb,
		statBarStyle.Render(humanBytes(bytes)),
		count,
		humanBytes(bytes),
		mutedStyle.Render("Items in the Trash cannot be restored after this."),
		helpStyle.Render("y / enter  confirm     n / esc  cancel"),
	)
	return confirmBoxStyle.Render(body)
}

func (m *model) viewCleaning() string {
	if m.pendingTrash {
		return m.viewCleaningTrash()
	}
	title := headerStyle.Render("spacestation")
	verb := "cleaning…"
	if m.cleanCancelled {
		verb = warnStyle.Render("cancelling… (finishing current step)")
	}
	count, bytes := m.countSelectedCleanable(), m.cleanableBytes()
	line := fmt.Sprintf("%s  %s  %s %d items, %s",
		title, m.spinner.View(), verb, count, humanBytes(bytes))
	hint := "please wait — Finder is moving files to Trash    " + mutedStyle.Render("esc cancel · q quit")
	if m.cleanCancelled {
		hint = mutedStyle.Render("waiting for the in-flight step to wind down…")
	}
	return "\n" + line + "\n\n" + helpStyle.Render(hint)
}

// viewCleaningTrash renders the permanent-Trash-removal screen with a live
// item count + bar, a running file counter, and a rolling log of the last few
// files removed. The bar tracks top-level items; the file counter climbs with
// every unlink so even one huge item visibly churns instead of looking stuck.
func (m *model) viewCleaningTrash() string {
	title := headerStyle.Render("spacestation")
	verb := "removing from Trash…"
	if m.cleanCancelled {
		verb = warnStyle.Render("cancelling… (finishing current item)")
	}
	_, bytes := m.trashTargetStats()
	total := m.trashTotal
	line := fmt.Sprintf("%s  %s  %s %d/%d items, %s",
		title, m.spinner.View(), verb, m.trashDone, total, humanBytes(bytes))

	bar := mutedStyle.Render(renderProgressBar(m.trashDone, total, 24))
	if m.trashFiles > 0 {
		bar += mutedStyle.Render(fmt.Sprintf("   · %s files removed", humanCount(m.trashFiles)))
	}

	// Rolling log: pad to 3 lines so the layout doesn't jump as it fills.
	logLines := make([]string, 0, 3)
	for _, name := range m.trashLog {
		logLines = append(logLines, mutedStyle.Render("  removed  ")+scanningStyle.Render(name))
	}
	for len(logLines) < 3 {
		logLines = append(logLines, mutedStyle.Render("  …"))
	}
	logBlock := strings.Join(logLines, "\n")

	hint := "please wait — permanently removing Trash items    " + mutedStyle.Render("esc cancel · q quit")
	if m.cleanCancelled {
		hint = mutedStyle.Render("waiting for the in-flight item to wind down…")
	}
	return "\n" + line + "\n\n  " + bar + "\n\n" + logBlock + "\n\n" + helpStyle.Render(hint)
}

// renderProgressBar draws a fixed-width [████░░░░] bar with a trailing percent.
// total <= 0 yields an indeterminate (all-empty) bar — we don't know the
// denominator yet, but the count line still moves.
func renderProgressBar(done, total, width int) string {
	pct := 0.0
	if total > 0 {
		pct = min(float64(done)/float64(total), 1)
	}
	filled := int(pct * float64(width))
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return fmt.Sprintf("%s  %3.0f%%", bar, pct*100)
}

func (m *model) viewDone() string {
	title := headerStyle.Render("spacestation")
	ok, fail := 0, 0
	for _, r := range m.cleanResults {
		if r.Err != nil {
			fail++
		} else {
			ok++
		}
	}
	verb := "moved to Trash"
	if m.pendingTrash {
		verb = "permanently removed from Trash"
	}
	// Empty-all returns a single aggregate result, so `ok` would read 1.
	// On full success the real item count is trashDone (also covers hidden
	// entries); use it so the summary matches the count shown while removing.
	items := ok
	if m.pendingTrash && m.trashEmptyAll && fail == 0 && m.trashDone > 0 {
		items = m.trashDone
	}
	var summary strings.Builder
	fmt.Fprintf(&summary, "  %s %s across %d items in %s",
		sizeStyle.Render(humanBytes(m.cleanedBytes)),
		verb,
		items,
		m.cleanElapsed.Truncate(100*time.Millisecond),
	)
	if fail > 0 {
		summary.WriteString("\n" + warnStyle.Render(fmt.Sprintf("  %d items failed:", fail)))
		for _, r := range m.cleanResults {
			if r.Err != nil {
				summary.WriteString("\n  " + mutedStyle.Render("• "+r.Candidate.DisplayTitle()+": "+r.Err.Error()))
			}
		}
	}
	return "\n" + title + "  ✓ done\n\n" + summary.String() + "\n\n" + helpStyle.Render("r rescan   q quit")
}

// helpers (padRight, padLeft live in format.go alongside truncatePath)
