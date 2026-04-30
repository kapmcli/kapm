package monitor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// tab identifiers
const (
	tabOverview = iota
	tabSessions
	tabAgents
	tabTools
	tabSkills
)

// default terminal dimensions
const (
	defaultWidth  = 120
	defaultHeight = 40
)

var tabNames = []string{"Overview", "Sessions", "Agents", "Tools", "Skills"}

var singleLineReplacer = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")

type metricsMsg struct {
	metrics DetailedMetrics
	cache   *SessionCache
	err     error
}

type tickMsg time.Time

type model struct {
	metrics     DetailedMetrics
	sessionsDir string
	hookLogsDir string
	ideBaseDir  string
	cwdFilter   string
	homeDir     string
	since       time.Duration

	ctx         context.Context
	cache       *SessionCache
	sqliteDBPath string
	sqliteCache  *SQLiteCache

	width  int
	height int
	err    error

	tab              int
	cursor           [5]int // per-tab selection
	detail           bool   // true when drilled into a detail view
	detailScroll     int    // top line of detail viewport
	cachedDetailMax  int    // cached result of detailMaxScroll computation
	cachedDetailBody string // cached rendered detail view
	updatedAt        time.Time
	promptExpanded   bool // toggle for prompt full display
	changesExpanded  bool // toggle for diff previews in Changes section
	timelineExpanded bool // toggle for full tool input in Timeline
}

// NewModel creates a new TUI model.
func NewModel(ctx context.Context, sessionsDir, hookLogsDir, ideBaseDir, cwdFilter string, since time.Duration) *model {
	// best-effort; empty string fallback is acceptable for path abbreviation
	home, _ := os.UserHomeDir()
	if ctx == nil {
		ctx = context.Background()
	}
	return &model{ctx: ctx, sessionsDir: sessionsDir, hookLogsDir: hookLogsDir, ideBaseDir: ideBaseDir, cwdFilter: cwdFilter, homeDir: home, since: since, width: defaultWidth, height: defaultHeight, cache: NewSessionCache(), sqliteCache: NewSQLiteCache()}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.refreshCmd(), tickCmd())
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg.String())
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.detail {
			m.recomputeDetailCache()
		}
	case metricsMsg:
		m.metrics = msg.metrics
		m.cache = msg.cache
		m.err = msg.err
		m.updatedAt = time.Now()
		m.clampCursors()
		if m.detail {
			m.recomputeDetailCache()
		}
	case tickMsg:
		return m, tea.Batch(m.refreshCmd(), tickCmd())
	}
	return m, nil
}

func (m *model) handleKey(key string) (tea.Model, tea.Cmd) {
	if m.detail {
		return m.handleDetailKey(key)
	}
	return m.handleListKey(key)
}

func (m *model) handleDetailKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "r":
		return m, m.refreshCmd()
	case "right", "l":
		n := m.listLen(m.tab)
		if m.cursor[m.tab] < n-1 {
			m.cursor[m.tab]++
			m.detailScroll = 0
			m.recomputeDetailCache()
		}
	case "left", "h":
		if m.cursor[m.tab] > 0 {
			m.cursor[m.tab]--
			m.detailScroll = 0
			m.recomputeDetailCache()
		}
	case "up", "k":
		if m.detailScroll > 0 {
			m.detailScroll--
		}
	case "down", "j":
		m.detailScroll = clampDetailScroll(m.detailScroll+1, m.detailMaxScroll())
	case "pgup":
		m.detailScroll -= m.viewportHeight() / 2
		if m.detailScroll < 0 {
			m.detailScroll = 0
		}
	case "pgdown":
		m.detailScroll = clampDetailScroll(m.detailScroll+m.viewportHeight()/2, m.detailMaxScroll())
	case "g", "home":
		m.detailScroll = 0
	case "G", "end":
		m.detailScroll = m.detailMaxScroll()
	case "esc", "backspace":
		m.detail = false
		m.detailScroll = 0
		m.recomputeDetailCache()
	case "p":
		m.promptExpanded = !m.promptExpanded
		m.recomputeDetailCache()
	case "d":
		m.changesExpanded = !m.changesExpanded
		m.recomputeDetailCache()
	case "t":
		m.timelineExpanded = !m.timelineExpanded
		m.recomputeDetailCache()
	}
	return m, nil
}

func (m *model) handleListKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "r":
		return m, m.refreshCmd()
	case "tab", "right", "l":
		m.tab = (m.tab + 1) % len(tabNames)
	case "shift+tab", "left", "h":
		m.tab = (m.tab - 1 + len(tabNames)) % len(tabNames)
	case "1":
		m.switchToTab(tabOverview)
	case "2":
		m.switchToTab(tabSessions)
	case "3":
		m.switchToTab(tabAgents)
	case "4":
		m.switchToTab(tabTools)
	case "5":
		m.switchToTab(tabSkills)
	case "up", "k":
		if m.tab != tabOverview && m.cursor[m.tab] > 0 {
			m.cursor[m.tab]--
		}
	case "down", "j":
		if m.tab != tabOverview {
			n := m.listLen(m.tab)
			if m.cursor[m.tab] < n-1 {
				m.cursor[m.tab]++
			}
		}
	case "g", "home":
		if m.tab != tabOverview {
			m.cursor[m.tab] = 0
		}
	case "G", "end":
		if m.tab != tabOverview {
			if n := m.listLen(m.tab); n > 0 {
				m.cursor[m.tab] = n - 1
			}
		}
	case "enter":
		if m.tab != tabOverview && m.listLen(m.tab) > 0 {
			m.detail = true
			m.detailScroll = 0
			m.recomputeDetailCache()
		}
	}
	return m, nil
}

func clampDetailScroll(v, max int) int {
	if v > max {
		return max
	}
	if v < 0 {
		return 0
	}
	return v
}

func (m *model) listLen(tab int) int {
	switch tab {
	case tabSessions:
		return len(m.metrics.Sessions)
	case tabAgents:
		return len(m.metrics.Agents)
	case tabTools:
		return len(m.metrics.Tools)
	case tabSkills:
		return len(m.metrics.Skills)
	}
	return 0
}

func (m *model) clampCursors() {
	for t := range m.cursor {
		n := m.listLen(t)
		if n == 0 {
			m.cursor[t] = 0
		} else if m.cursor[t] >= n {
			m.cursor[t] = n - 1
		}
	}
	if m.listLen(m.tab) == 0 {
		m.detail = false
	}
}

func (m *model) View() tea.View {
	v := tea.NewView(m.renderView())
	v.AltScreen = true
	return v
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 1)

	tabStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B6B6B")).
			Padding(0, 2)

	tabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 2)

	sectionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#04B575"))

	mutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B6B6B"))

	activeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575")).Bold(true)

	doneStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B6B6B"))

	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E06C75")).Bold(true)

	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5C07B"))

	barStyleOK = lipgloss.NewStyle().Foreground(lipgloss.Color("#61AFEF"))

	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#3A3A3A")).
			Padding(0, 1)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#3A3A5F")).
			Bold(true)

	helpStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B6B6B")).Italic(true)

	addStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	delStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // red
	hunkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan for @@ headers
)

func (m *model) renderView() string {
	if m.err != nil {
		return errorStyle.Render(fmt.Sprintf("Error: %v", m.err)) + "\n\n" + helpStyle.Render("Press q to quit.")
	}

	var b strings.Builder
	b.WriteString(m.renderTopBar())
	b.WriteString("\n")
	b.WriteString(m.renderTabs())
	b.WriteString("\n\n")
	b.WriteString(m.renderBody())
	b.WriteString("\n")
	b.WriteString(helpStyle.Render(m.helpLine()))
	return b.String()
}

func (m *model) renderTopBar() string {
	title := titleStyle.Render(" kapm monitor ")
	period := mutedStyle.Render(fmt.Sprintf("period: last %s", formatDur(m.since)))
	updated := "—"
	if v := os.Getenv("KAPM_UPDATED_AT"); v != "" {
		updated = v
	} else if !m.updatedAt.IsZero() {
		updated = m.updatedAt.Format("15:04:05")
	}
	right := mutedStyle.Render(fmt.Sprintf("updated: %s", updated))
	return lipgloss.JoinHorizontal(lipgloss.Left, title, "  ", period, "  ", right)
}

func (m *model) renderTabs() string {
	parts := make([]string, len(tabNames))
	for i, name := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if i == m.tab {
			parts[i] = tabActiveStyle.Render(label)
		} else {
			parts[i] = tabStyle.Render(label)
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, parts...)
}

func (m *model) renderBody() string {
	if len(m.metrics.Overview.Sessions) == 0 {
		return mutedStyle.Render("No log data found in " + m.sessionsDir)
	}
	switch m.tab {
	case tabOverview:
		return m.renderOverview()
	case tabSessions:
		if m.detail {
			return m.scrollDetail(m.cachedDetailBody)
		}
		return m.renderSessionsList()
	case tabAgents:
		if m.detail {
			return m.scrollDetail(m.cachedDetailBody)
		}
		return m.renderAgentsList()
	case tabTools:
		if m.detail {
			return m.scrollDetail(m.cachedDetailBody)
		}
		return m.renderToolsList()
	case tabSkills:
		if m.detail {
			return m.scrollDetail(m.cachedDetailBody)
		}
		return m.renderSkillsTab()
	}
	return ""
}

// scrollDetail slices the rendered detail body according to m.detailScroll and
// viewportHeight, appending a footer hint if content is below, then wraps in
// the standard content border.
func (m *model) scrollDetail(body string) string {
	if body == "" {
		return body
	}
	lines := strings.Split(body, "\n")
	vh := m.viewportHeight()
	total := len(lines)
	var out string
	if total <= vh {
		out = body
	} else {
		start := m.detailScroll
		max := total - vh
		if start > max {
			start = max
		}
		if start < 0 {
			start = 0
		}
		end := min(start+vh, total)
		out = strings.Join(lines[start:end], "\n")
		if remaining := total - end; remaining > 0 {
			out += "\n" + mutedStyle.Render(fmt.Sprintf("(%d more lines, ↓ to scroll)", remaining))
		}
	}
	return borderStyle.Width(m.contentWidth()).Render(out)
}

// viewportHeight is the number of lines available for detail content.
func (m *model) viewportHeight() int {
	n := max(m.height-8, 5)
	return n
}

// detailMaxScroll returns the cached max scroll offset for the current detail view.
func (m *model) detailMaxScroll() int {
	return m.cachedDetailMax
}

// switchToTab resets the model to the given tab's list view.
func (m *model) switchToTab(tab int) {
	m.tab = tab
	m.detail = false
	m.detailScroll = 0
	m.recomputeDetailCache()
}

// recomputeDetailCache recomputes and stores the rendered detail body and max scroll offset.
func (m *model) recomputeDetailCache() {
	if !m.detail {
		m.cachedDetailMax = 0
		m.cachedDetailBody = ""
		return
	}
	switch m.tab {
	case tabSessions:
		m.cachedDetailBody = m.renderSessionDetail()
	case tabAgents:
		m.cachedDetailBody = m.renderAgentDetail()
	case tabTools:
		m.cachedDetailBody = m.renderToolDetail()
	case tabSkills:
		m.cachedDetailBody = m.renderSkillDetail()
	}
	if m.cachedDetailBody == "" {
		m.cachedDetailMax = 0
		return
	}
	total := strings.Count(m.cachedDetailBody, "\n") + 1
	max := total - m.viewportHeight()
	if max < 0 {
		max = 0
	}
	m.cachedDetailMax = max
}

func (m *model) helpLine() string {
	if m.detail {
		return "←→/hl: prev/next · ↑↓/jk: scroll · pgup/pgdn: page · p: prompts · d: diffs · t: tool input · esc: back · q: quit"
	}
	base := "tab/←→: switch · 1-5: jump · q: quit · r: refresh"
	if m.tab == tabOverview {
		return base
	}
	return base + " · ↑↓/jk: select · enter: open"
}

func (m *model) refreshCmd() tea.Cmd {
	cache := m.cache
	sessionsDir := m.sessionsDir
	logsDir := m.hookLogsDir
	ideBaseDir := m.ideBaseDir
	cwdFilter := m.cwdFilter
	sinceDur := m.since
	ctx := m.ctx
	sqliteDBPath := m.sqliteDBPath
	sqliteCache := m.sqliteCache
	return func() tea.Msg {
		since := time.Now().Add(-sinceDur)
		records, nextCache, err := LoadAll(ctx, sessionsDir, logsDir, ideBaseDir, sqliteDBPath, since, cwdFilter, cache, sqliteCache)
		if err != nil {
			return metricsMsg{err: err}
		}
		dm, err := AggregateDetail(ctx, records, time.Now())
		if err != nil {
			return metricsMsg{err: err}
		}
		return metricsMsg{metrics: dm, cache: nextCache}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// --- helpers -----------------------------------------------------------------

func barChart(value, max, maxWidth int) string {
	if max == 0 {
		return strings.Repeat(" ", maxWidth)
	}
	w := (value * maxWidth) / max
	if w == 0 && value > 0 {
		w = 1
	}
	return strings.Repeat("█", w) + strings.Repeat(" ", maxWidth-w)
}

func formatDur(d time.Duration) string { return FormatDuration(d) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// shortID returns up to n runes of an ID. Session IDs in live data look like
// UUIDs; 12 chars preserves enough to distinguish sessions.
func shortID(id string, n int) string {
	if len(id) <= n {
		return id
	}
	return id[:n]
}

// singleLine collapses newlines/tabs to spaces for list rows.
func singleLine(s string) string {
	return strings.Join(strings.Fields(singleLineReplacer.Replace(s)), " ")
}

// padRightVisible pads a string (possibly containing ANSI escape codes) with
// spaces on the right so the visible width is at least n.
func padRightVisible(s string, n int) string {
	visible := lipgloss.Width(s)
	if visible >= n {
		return s
	}
	return s + strings.Repeat(" ", n-visible)
}

// statusBadge returns a colored active/done badge.
func statusBadge(active bool) string {
	if active {
		return activeStyle.Render("● active")
	}
	return doneStyle.Render("○ done")
}

// RunTUI creates and runs the bubbletea program.
func RunTUI(ctx context.Context, sessionsDir, hookLogsDir, ideBaseDir, cwdFilter, sqliteDBPath string, since time.Duration) error {
	m := NewModel(ctx, sessionsDir, hookLogsDir, ideBaseDir, cwdFilter, since)
	m.sqliteDBPath = sqliteDBPath
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}
