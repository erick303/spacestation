package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/erick303/spacestation/internal/cleanup"
	"github.com/erick303/spacestation/internal/config"
	"github.com/erick303/spacestation/internal/scan"
	"github.com/erick303/spacestation/internal/score"
)

// Public entrypoint.
func Run(cfg config.Config, hardDelete bool) error {
	m := newModel(cfg, hardDelete)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
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
	isHeader bool
	cat      scan.Category
	candIdx  int  // index into model.cands when !isHeader
	collapsed bool // for headers
}

type model struct {
	cfg        config.Config
	hardDelete bool

	stage stage

	// scanning state
	spinner       spinner.Model
	scanStart     time.Time
	scanElapsed   time.Duration
	progressMsg   string
	progressFound int
	progressBytes int64
	scanDone      bool
	progressCh    chan scan.Progress
	cands         []scan.Candidate

	// browsing state
	rows        []row
	cursor      int
	collapsed   map[scan.Category]bool
	width       int
	height      int
	flash       string
	flashUntil  time.Time

	// cleaning state
	cleanStart   time.Time
	cleanElapsed time.Duration
	cleanResults []cleanup.Result
	cleanedBytes int64

	// "press space again to confirm group toggle" arm state
	armedGroupCat    scan.Category
	armedGroupActive bool
	armedExpiry      time.Time

	// dashboard
	dashboardOn bool
	diskUsage   scan.DiskUsage
}

func newModel(cfg config.Config, hard bool) *model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorAccent)
	return &model{
		cfg:         cfg,
		hardDelete:  hard,
		stage:       stageScanning,
		spinner:     sp,
		scanStart:   time.Now(),
		collapsed:   map[scan.Category]bool{},
		progressCh:  make(chan scan.Progress, 64),
		dashboardOn: true,
	}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.startScan(),
		m.pollProgress(),
		tickEvery(),
	)
}

// --- messages ---

type scanProgressMsg scan.Progress
type scanDoneMsg struct {
	cands   []scan.Candidate
	elapsed time.Duration
}
type cleanDoneMsg struct {
	results []cleanup.Result
	elapsed time.Duration
	bytes   int64
}
type tickMsg time.Time

func tickEvery() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) startScan() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		start := time.Now()
		cands := scan.Run(ctx, scan.Options{Cfg: m.cfg}, m.progressCh)
		score.Apply(cands, m.cfg)
		// Persist size-cache so subsequent scans skip walking unchanged trees.
		_ = scan.SaveSizeCache()
		return scanDoneMsg{cands: cands, elapsed: time.Since(start)}
	}
}

func (m *model) pollProgress() tea.Cmd {
	return func() tea.Msg {
		p, ok := <-m.progressCh
		if !ok {
			return nil
		}
		return scanProgressMsg(p)
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
		m.progressMsg = msg.Message
		if msg.Found > 0 {
			m.progressFound = msg.Found
			m.progressBytes = msg.Bytes
		}
		return m, m.pollProgress()

	case scanDoneMsg:
		m.cands = msg.cands
		m.scanElapsed = msg.elapsed
		m.scanDone = true
		m.diskUsage = scan.GetDiskUsage("/")
		m.rebuildRows()
		m.stage = stageBrowsing
		return m, nil

	case cleanDoneMsg:
		m.cleanResults = msg.results
		m.cleanedBytes = msg.bytes
		m.cleanElapsed = msg.elapsed
		m.stage = stageDone
		return m, nil
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.stage {
	case stageScanning:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
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
			return m, m.executeClean()
		case "n", "N", "esc", "q":
			m.stage = stageBrowsing
			return m, nil
		}
		return m, nil

	case stageCleaning:
		return m, nil

	case stageDone:
		switch msg.String() {
		case "q", "enter", "esc", "ctrl+c":
			return m, tea.Quit
		case "r":
			// rescan
			*m = *newModel(m.cfg, m.hardDelete)
			return m, m.Init()
		}
		return m, nil
	}
	return m, nil
}

func (m *model) handleBrowseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
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
		m.selectAll(false)
	case "tab":
		m.toggleCollapseAtCursor()
	case "enter":
		// Enter always means "clean". Tab is for collapse.
		if m.countSelected() > 0 {
			m.stage = stageConfirm
		} else {
			m.setFlash("No items selected. Press space to select, A for all, then enter.")
		}
	case "r":
		// rescan
		*m = *newModel(m.cfg, m.hardDelete)
		return m, m.Init()
	case "v":
		m.dashboardOn = !m.dashboardOn
	}
	return m, nil
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

func (m *model) countSelected() int {
	n := 0
	for _, c := range m.cands {
		if c.Selected {
			n++
		}
	}
	return n
}

func (m *model) selectedBytes() int64 {
	var n int64
	for _, c := range m.cands {
		if c.Selected {
			n += c.SizeBytes
		}
	}
	return n
}

func (m *model) totalBytes() int64 {
	var n int64
	for _, c := range m.cands {
		n += c.SizeBytes
	}
	return n
}

func (m *model) groupStats(cat scan.Category) (count int, bytes int64) {
	for _, c := range m.cands {
		if c.Category == cat {
			count++
			bytes += c.SizeBytes
		}
	}
	return
}

func (m *model) groupSelectedStats(cat scan.Category) (count int, bytes int64) {
	for _, c := range m.cands {
		if c.Category == cat && c.Selected {
			count++
			bytes += c.SizeBytes
		}
	}
	return
}

func (m *model) setFlash(s string) {
	m.flash = s
	m.flashUntil = time.Now().Add(3 * time.Second)
}

func (m *model) executeClean() tea.Cmd {
	return func() tea.Msg {
		var selected []scan.Candidate
		var bytes int64
		for _, c := range m.cands {
			if c.Selected {
				selected = append(selected, c)
				bytes += c.SizeBytes
			}
		}
		mode := cleanup.ModeTrash
		if m.hardDelete || m.cfg.Delete.Mode == "hard" {
			mode = cleanup.ModeHard
		}
		start := time.Now()
		results := cleanup.Execute(selected, mode)
		// Invalidate size-cache entries for what we successfully removed so the
		// next scan re-measures them.
		for _, r := range results {
			if r.Err == nil && r.Candidate.Action == scan.ActionDelete {
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
	helpLine2 := "tab collapse  [ / ] prev/next group  v dashboard  enter clean  r rescan  q quit"
	help := helpStyle.Render(helpLine1 + "\n" + helpLine2)

	flashLine := ""
	if m.flash != "" && time.Now().Before(m.flashUntil) {
		flashLine = warnStyle.Render("  ⚠ " + m.flash)
	}

	// detail pane has 3 lines (action, detail, safety+reason)
	detail := m.renderDetail(width)

	dashboard := ""
	dashboardLines := 0
	if m.dashboardOn {
		dashboard = renderDashboard(width, m.diskUsage, m.cands)
		// Count actual lines — breakdown wraps when many categories or narrow term.
		dashboardLines = strings.Count(dashboard, "\n") + 2 // self + blank below
	}

	// Account for everything that's NOT the list:
	//   header(1) + blank(1) [+ dashboard(3) + blank(1)]
	// + blank-before-detail(1) + detail(3)
	// + blank-before-help(1) + helpStyle.PaddingTop(1) + help(2 lines)
	// + flash(1 when present)
	reserved := 11 + dashboardLines
	if flashLine != "" {
		reserved += 1
	}
	viewportHeight := m.height - reserved
	if viewportHeight < 5 {
		viewportHeight = 5
	}
	listView := m.renderList(width, viewportHeight)

	parts := []string{header, ""}
	if dashboard != "" {
		parts = append(parts, dashboard, "")
	}
	parts = append(parts, listView, detail)
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

	// simple windowed view
	start := 0
	end := len(m.rows)
	if len(m.rows) > height {
		// keep cursor in view
		if m.cursor < height/2 {
			start = 0
		} else if m.cursor > len(m.rows)-height/2 {
			start = len(m.rows) - height
		} else {
			start = m.cursor - height/2
		}
		end = start + height
		if end > len(m.rows) {
			end = len(m.rows)
		}
	}

	// Render at most `height` output lines so the surrounding chrome (top
	// status, dashboard, detail, help) stays visible. The blank line between
	// groups counts toward this budget.
	var b strings.Builder
	linesOut := 0
	for i := start; i < end && linesOut < height; i++ {
		isCursor := i == m.cursor
		r := m.rows[i]
		if i > start && r.isHeader && linesOut < height-1 {
			b.WriteString("\n")
			linesOut++
		}
		var line string
		if r.isHeader {
			line = m.renderHeaderRow(r, isCursor)
		} else {
			line = m.renderItemRow(width, m.cands[r.candIdx], isCursor)
		}
		b.WriteString(line)
		b.WriteString("\n")
		linesOut++
	}
	return b.String()
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
	cb := checkboxOff
	if c.Selected {
		cb = checkboxOn
	}
	// "  ●  " on the left = leftPad(2) + dot(1) + space(2) ; the dot stays its
	// own color even when the row is highlighted because it's rendered outside
	// the selection style.
	dot := categoryStyle(c.Category).Render("●")

	pathW := width - 38
	if pathW < 20 {
		pathW = 20
	}
	label := c.DisplayTitle()
	if c.Action == scan.ActionDelete {
		label = homeRelative(label)
	}
	tag := ""
	if c.Action == scan.ActionCommand {
		tag = "⚡ "
	}
	path := truncatePath(tag+label, pathW)
	paddedPath := padRight(path, pathW)
	if c.Action == scan.ActionCommand {
		// Style only the visible title; trailing spaces stay unstyled so they
		// don't get bold-yellow under the size/age columns.
		if len(paddedPath) > len(path) {
			paddedPath = smartTitleStyle.Render(path) + paddedPath[len(path):]
		} else {
			paddedPath = smartTitleStyle.Render(paddedPath)
		}
	}
	inner := fmt.Sprintf("%s %s  %s   %s",
		cb,
		paddedPath,
		sizeStyle.Render(padLeft(humanBytes(c.SizeBytes), 9)),
		ageStyle.Render(padLeft(humanAge(c.LastTouched), 9)),
	)
	if isCursor {
		inner = itemSelectedStyle.Render(inner)
	}
	indent := "  "
	if isCursor {
		indent = cursorArrowStyle.Render("▸ ")
	}
	return indent + dot + "  " + inner
}

func (m *model) renderDetail(width int) string {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return ""
	}
	r := m.rows[m.cursor]
	if r.isHeader {
		return mutedStyle.Render("  " + r.cat.String() + " group — press tab to collapse, a to select all in group")
	}
	c := m.cands[r.candIdx]
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
	verb := "Move to Trash"
	hint := "Items will go to ~/.Trash — you can restore from Finder."
	if m.hardDelete || m.cfg.Delete.Mode == "hard" {
		verb = dangerStyle.Render("PERMANENTLY DELETE")
		hint = "--hard mode: items will be removed immediately. No undo."
	}
	body := fmt.Sprintf(
		"%s  %s\n\n%d items, %s\n\n%s\n\n%s",
		verb,
		statBarStyle.Render(humanBytes(m.selectedBytes())),
		m.countSelected(),
		humanBytes(m.selectedBytes()),
		mutedStyle.Render(hint),
		helpStyle.Render("y / enter  confirm     n / esc  cancel"),
	)
	return confirmBoxStyle.Render(body)
}

func (m *model) viewCleaning() string {
	title := headerStyle.Render("spacestation")
	line := fmt.Sprintf("%s  %s  cleaning… %d items, %s",
		title, m.spinner.View(), m.countSelected(), humanBytes(m.selectedBytes()))
	return "\n" + line + "\n\n" + helpStyle.Render("please wait — Finder is moving files to Trash")
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
	if m.hardDelete || m.cfg.Delete.Mode == "hard" {
		verb = "permanently deleted"
	}
	summary := fmt.Sprintf("  %s %s across %d items in %s",
		sizeStyle.Render(humanBytes(m.cleanedBytes)),
		verb,
		ok,
		m.cleanElapsed.Truncate(100*time.Millisecond),
	)
	if fail > 0 {
		summary += "\n" + warnStyle.Render(fmt.Sprintf("  %d items failed:", fail))
		for _, r := range m.cleanResults {
			if r.Err != nil {
				summary += "\n  " + mutedStyle.Render("• "+r.Candidate.DisplayTitle()+": "+r.Err.Error())
			}
		}
	}
	return "\n" + title + "  ✓ done\n\n" + summary + "\n\n" + helpStyle.Render("r rescan   q quit")
}

// helpers
func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
func padLeft(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return strings.Repeat(" ", n-len(s)) + s
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
