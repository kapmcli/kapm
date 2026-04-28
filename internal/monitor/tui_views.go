package monitor

import (
	"cmp"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	udiff "github.com/aymanbagabas/go-udiff"
	"github.com/kapmcli/kapm/internal/apmconfig"
)

const tsLayout = "2006-01-02 15:04:05"

const (
	overviewTopN      = 10
	maxRecentSessions = 10
)

// --- Overview ----------------------------------------------------------------

func (m *model) renderOverview() string {
	full := m.contentWidth()
	// Row 1: Summary (+ Activity if non-empty) side-by-side 50/50.
	var row1 string
	if len(m.metrics.Overview.HourlyActivity) == 0 {
		row1 = m.renderSummaryBox(full)
	} else {
		ws := splitBoxWidths(full, 2, 1)
		row1 = lipgloss.JoinHorizontal(lipgloss.Top,
			m.renderSummaryBox(ws[0]), " ", m.renderActivityBox(ws[1]))
	}

	// Row 2: Top tools + Top agents (+ Skills if non-empty) as 2 or 3 cols.
	var row2 string
	if len(m.metrics.Skills) == 0 {
		ws := splitBoxWidths(full, 2, 1)
		row2 = lipgloss.JoinHorizontal(lipgloss.Top,
			m.renderTopTools(overviewTopN, ws[0]), " ", m.renderTopAgents(overviewTopN, ws[1]))
	} else {
		ws := splitBoxWidths(full, 3, 1)
		row2 = lipgloss.JoinHorizontal(lipgloss.Top,
			m.renderTopTools(overviewTopN, ws[0]), " ",
			m.renderTopAgents(overviewTopN, ws[1]), " ",
			m.renderTopSkills(overviewTopN, ws[2]))
	}

	// Row 3: Recent active sessions full width.
	row3 := m.renderRecentSessionsBox(full)

	return row1 + "\n" + row2 + "\n" + row3
}

// splitBoxWidths returns n outer-widths that sum to total, separated by gap
// cells between boxes. Remainder is distributed to leftmost boxes.
func splitBoxWidths(total, n, gap int) []int {
	if n <= 0 {
		return nil
	}
	avail := total - gap*(n-1)
	if avail < n*10 {
		avail = n * 10
	}
	base := avail / n
	rem := avail - base*n
	out := make([]int, n)
	for i := range out {
		out[i] = base
		if i < rem {
			out[i]++
		}
	}
	return out
}

// interiorOf returns the interior content width of a box with outer width w.
// Border + padding consume 4 cells.
func interiorOf(w int) int {
	if w-4 < 10 {
		return 10
	}
	return w - 4
}

func errorCountText(n int) string {
	if n == 0 {
		return mutedStyle.Render("0")
	}
	return errorStyle.Render(fmt.Sprintf("%d", n))
}

// renderSummaryBox draws the Summary panel with logs dir + counters, fitted to width.
func (m *model) renderSummaryBox(width int) string {
	ov := m.metrics.Overview
	active := 0
	totalTools := 0
	totalPrompts := 0
	for _, s := range ov.Sessions {
		if s.Active {
			active++
		}
		totalTools += s.ToolCalls
		totalPrompts += s.Prompts
	}
	totalErrors := 0
	for _, t := range ov.Tools {
		totalErrors += t.ErrorCount
	}

	interior := interiorOf(width)
	logsDir := abbrevHome(m.homeDir, m.hookLogsDir)
	// Reserve 12 chars for "logs dir:  " prefix.
	if n := interior - len("logs dir: "); n > 0 && len(logsDir) > n {
		logsDir = truncateLeft(logsDir, n)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", sectionStyle.Render("▸ Summary"))
	fmt.Fprintf(&b, "  logs dir: %s\n", mutedStyle.Render(logsDir))
	fmt.Fprintf(&b, "  sessions:   %-5d  active: %s\n",
		len(ov.Sessions), activeStyle.Render(fmt.Sprintf("%d", active)))
	fmt.Fprintf(&b, "  agents:     %-5d  errors: %s\n",
		len(ov.Agents), errorCountText(totalErrors))
	fmt.Fprintf(&b, "  tool calls: %-5d  prompts: %d",
		totalTools, totalPrompts)
	return borderStyle.Width(width).Render(b.String())
}

func (m *model) renderTopTools(limit, width int) string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("▸ Top tools"))
	b.WriteString("\n")

	tools := m.metrics.Overview.Tools
	if len(tools) == 0 {
		b.WriteString(mutedStyle.Render("  no tool usage"))
		return borderStyle.Width(width).Render(b.String())
	}
	if len(tools) < limit {
		limit = len(tools)
	}
	// Reserve: 2(indent) + 12(name) + 1 + bar + 1 + 4(count) + 5(" err:") + 6(err%) = 31 + bar
	interior := interiorOf(width)
	barW := interior - 31
	if barW < 3 {
		barW = 3
	}
	if barW > 20 {
		barW = 20
	}
	maxCall := tools[0].CallCount
	for i := range limit {
		t := tools[i]
		bar := barChart(t.CallCount, maxCall, barW)
		fmt.Fprintf(&b, "  %-12s %s %4d err:%s\n",
			truncate(t.Name, 12), barStyleOK.Render(bar), t.CallCount, formatErrRate(t.ErrorRate))
	}
	return borderStyle.Width(width).Render(b.String())
}

func (m *model) renderTopAgents(limit, width int) string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("▸ Top agents"))
	b.WriteString("\n")

	agents := m.metrics.Overview.Agents
	if len(agents) == 0 {
		b.WriteString(mutedStyle.Render("  no agent activity"))
		return borderStyle.Width(width).Render(b.String())
	}
	if len(agents) < limit {
		limit = len(agents)
	}
	// Reserve 2(indent) + 1 + 6(Sess) + 1 + 5(Tool) + 1 + 6(Prompt) = 22
	interior := interiorOf(width)
	nameW := interior - 22
	if nameW < 8 {
		nameW = 8
	}
	if nameW > 30 {
		nameW = 30
	}
	fmt.Fprintf(&b, "  %-*s %6s %5s %6s\n", nameW, "Name", "Sess", "Tools", "Prompt")
	for i := range limit {
		a := agents[i]
		fmt.Fprintf(&b, "  %-*s %6d %5d %6d\n",
			nameW, truncate(a.Name, nameW), a.SessionCount, a.ToolCalls, a.Prompts)
	}
	return borderStyle.Width(width).Render(b.String())
}

func (m *model) renderActivityBox(width int) string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("▸ Activity"))
	b.WriteString("\n")

	hours := m.metrics.Overview.HourlyActivity
	if len(hours) == 0 {
		b.WriteString(mutedStyle.Render("  no activity"))
		return borderStyle.Width(width).Render(b.String())
	}
	maxEv := 0
	for _, h := range hours {
		if h.EventCount > maxEv {
			maxEv = h.EventCount
		}
	}
	// Each column is 2 cells wide (bar + space). Fit to interior.
	interior := interiorOf(width) - 2 // leave 2 for leading "  " indent
	maxCols := interior / 2
	if maxCols < 1 {
		maxCols = 1
	}
	if len(hours) > maxCols {
		hours = hours[len(hours)-maxCols:]
	}

	bars := []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	var spark, labels strings.Builder
	for i, h := range hours {
		idx := 0
		if maxEv > 0 {
			idx = (h.EventCount * (len(bars) - 1)) / maxEv
		}
		spark.WriteRune(bars[idx])
		spark.WriteRune(' ')
		if i%3 == 0 {
			labels.WriteString(h.Hour.Local().Format("15"))
		} else {
			labels.WriteString("  ")
		}
	}
	fmt.Fprintf(&b, "  %s\n  %s\n", barStyleOK.Render(spark.String()), mutedStyle.Render(labels.String()))
	fmt.Fprintf(&b, "  max: %d events/hour", maxEv)
	return borderStyle.Width(width).Render(b.String())
}

func (m *model) renderTopSkills(limit, width int) string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("▸ Skills (reads)"))
	b.WriteString("\n")
	skills := m.metrics.Skills
	if len(skills) == 0 {
		b.WriteString(mutedStyle.Render("  no skill reads"))
		return borderStyle.Width(width).Render(b.String())
	}
	if len(skills) < limit {
		limit = len(skills)
	}
	maxC := skills[0].ReadCount
	// Reserve 2(indent) + 18(name) + 1 + bar + 1 + 4(count) = 26 + bar
	interior := interiorOf(width)
	barW := interior - 26
	if barW < 3 {
		barW = 3
	}
	if barW > 20 {
		barW = 20
	}
	for i := range limit {
		sk := skills[i]
		bar := barChart(sk.ReadCount, maxC, barW)
		fmt.Fprintf(&b, "  %-18s %s %4d\n",
			truncate(sk.Name, 18), barStyleOK.Render(bar), sk.ReadCount)
	}
	return borderStyle.Width(width).Render(b.String())
}

// renderRecentSessionsBox renders a full-width box showing top N sessions
// (already sorted by LastActivity desc) with a detailed row per session.
func (m *model) renderRecentSessionsBox(width int) string {
	sessions := m.metrics.Sessions
	if len(sessions) > maxRecentSessions {
		sessions = sessions[:maxRecentSessions]
	}
	interior := interiorOf(width)

	var b strings.Builder
	b.WriteString(sectionStyle.Render("▸ Recent active sessions"))
	b.WriteString("\n")
	if len(sessions) == 0 {
		b.WriteString(mutedStyle.Render("  (none)"))
		return borderStyle.Width(width).Render(b.String())
	}

	// Fixed chars: 2(indent) + 12(ID) + 2 + 8(Dur) + 2 + 9(Status) + 2 + 5(Tools) + 2 + 7(Prompts) + 2 + 11(Last act) = 64
	fixed := 2 + 12 + 2 + 8 + 2 + 9 + 2 + 5 + 2 + 7 + 2 + 11
	remaining := interior - fixed - 2 // 2 spaces between ID and Agent
	titleW := 40
	agentW := remaining - 2 - titleW // 2 spaces between Agent and Title
	if agentW < 10 {
		agentW = 10
	}
	if agentW > 16 {
		agentW = 16
	}

	fmt.Fprintf(&b, "  %-12s  %-*s  %-*s  %-8s  %-9s  %5s  %7s  %-11s\n",
		"ID", agentW, "Agent", titleW, "Title", "Duration", "Status", "Tools", "Prompts", "Last act")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", interior)))
	b.WriteString("\n")
	now := time.Now()
	var prevID string
	for _, s := range sessions {
		idCell := shortID(s.ID, 12)
		agentCell := truncate(s.Agent, agentW)
		if s.ID == prevID {
			idCell = strings.Repeat(" ", 12)
			agentCell = "  " + truncate(s.Agent, agentW-2)
		}
		prevID = s.ID
		titleCell := truncateVisible(cmp.Or(s.Title, "—"), titleW)
		fmt.Fprintf(&b, "  %-12s  %-*s  %s  %-8s  %s  %5d  %7d  %-11s\n",
			idCell,
			agentW, agentCell,
			padRightVisible(titleCell, titleW),
			formatDur(time.Duration(s.Duration)),
			padRightVisible(statusBadge(s.Active), 9),
			s.ToolCalls, s.Prompts,
			formatLastActivity(s.LastActivity, now),
		)
	}
	return borderStyle.Width(width).Render(b.String())
}

// abbrevHome replaces the user's home-directory prefix with "~".
func abbrevHome(home, p string) string {
	if home == "" {
		return p
	}
	home = filepath.Clean(home)
	p = filepath.Clean(p)
	if samePathText(p, home) {
		return "~"
	}
	if suffix, ok := trimPathPrefix(p, home); ok {
		return "~" + string(filepath.Separator) + suffix
	}
	return p
}

func trimPathPrefix(p, prefix string) (string, bool) {
	prefix = strings.TrimRight(prefix, `/\`)
	if prefix == "" || len(p) <= len(prefix) {
		return "", false
	}
	if !samePathText(p[:len(prefix)], prefix) || !isPathSeparator(p[len(prefix)]) {
		return "", false
	}
	return p[len(prefix)+1:], true
}

func samePathText(a, b string) bool {
	a = strings.ReplaceAll(a, `\`, `/`)
	b = strings.ReplaceAll(b, `\`, `/`)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func isPathSeparator(ch byte) bool {
	return ch == '/' || ch == '\\'
}

// truncateLeft returns a string whose visible length is <= n, keeping the
// right-most characters and prefixing with "…" when truncated.
func truncateLeft(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return "…" + s[len(s)-(n-1):]
}

// --- Sessions list ----------------------------------------------------------

func (m *model) renderSessionsList() string {
	sessions := m.metrics.Sessions
	interior := m.interiorWidth()

	// Fixed: 2(indent) + 12(ID) + 1 + agent + 1 + title + 1 + 8(Dur) + 1 + 9(Status) + 1 + 4(Tool) + 1 + 5(Prompt) + 1 + 5(Files) + 1 + 11(Last act)
	titleW := 40
	fixed := 2 + 12 + 1 + 1 + titleW + 1 + 8 + 1 + 9 + 1 + 4 + 1 + 5 + 1 + 5 + 1 + 11
	agentW := interior - fixed
	if agentW < 10 {
		agentW = 10
	}
	if agentW > 16 {
		agentW = 16
	}

	var b strings.Builder
	fmt.Fprintf(&b, "  %-12s %-*s %-*s %-8s %-9s %4s %5s %5s %-11s\n",
		"ID", agentW, "Agent", titleW, "Title", "Dur", "Status", "Tool", "Prompt", "Files", "Last act")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", interior)))
	b.WriteString("\n")

	rows := m.viewportHeight()
	start := clampOffset(m.cursor[tabSessions], len(sessions), rows)
	end := start + rows
	if end > len(sessions) {
		end = len(sessions)
	}
	now := time.Now()
	var prevID string
	for i := start; i < end; i++ {
		s := sessions[i]
		idCell := shortID(s.ID, 12)
		agentCell := truncate(s.Agent, agentW)
		if s.ID == prevID {
			idCell = strings.Repeat(" ", 12)
			agentCell = "  " + truncate(s.Agent, agentW-2)
		}
		prevID = s.ID
		titleCell := truncateVisible(cmp.Or(s.Title, "—"), titleW)
		status := statusBadge(s.Active)
		row := fmt.Sprintf("  %-12s %-*s %s %-8s %s %4d %5d %5d %-11s",
			idCell,
			agentW, agentCell,
			padRightVisible(titleCell, titleW),
			formatDur(time.Duration(s.Duration)),
			padRightVisible(status, 9),
			s.ToolCalls, s.Prompts, s.FilesChanged,
			formatLastActivity(s.LastActivity, now),
		)
		if i == m.cursor[tabSessions] {
			b.WriteString(selectedStyle.Render("▸ " + row[2:]))
		} else {
			b.WriteString(row)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n%s  %d/%d", mutedStyle.Render("showing"), end-start, len(sessions))
	return borderStyle.Width(m.contentWidth()).Render(b.String())
}

// formatLastActivity renders t as HH:MM:SS when same local day as now, otherwise MM-DD HH:MM.
func formatLastActivity(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	local := t.Local()
	nowLocal := now.Local()
	if local.Year() == nowLocal.Year() && local.YearDay() == nowLocal.YearDay() {
		return local.Format("15:04:05")
	}
	return local.Format("01-02 15:04")
}

// --- Session detail ---------------------------------------------------------

func (m *model) renderSessionDetail() string {
	sessions := m.metrics.Sessions
	if len(sessions) == 0 {
		return ""
	}
	idx := m.cursor[tabSessions]
	if idx >= len(sessions) {
		idx = len(sessions) - 1
	}
	s := &sessions[idx]
	return m.renderSessionHeader(s) +
		m.renderSessionToolSummary(s) +
		m.renderSessionAssistantResponse(s) +
		m.renderSessionChanges(s) +
		m.renderSessionPrompts(s) +
		m.renderSessionTimeline(s)
}

func (m *model) renderSessionHeader(s *SessionDetail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", sectionStyle.Render("Session"), s.ID)
	fmt.Fprintf(&b, "  title:    %s\n", cmp.Or(s.Title, "—"))
	fmt.Fprintf(&b, "  agent:    %s\n", s.Agent)
	fmt.Fprintf(&b, "  cwd:      %s\n", cmp.Or(s.Cwd, "—"))
	fmt.Fprintf(&b, "  started:  %s\n", s.StartTime.Local().Format(tsLayout))
	fmt.Fprintf(&b, "  ended:    %s\n", s.EndTime.Local().Format(tsLayout))
	fmt.Fprintf(&b, "  duration: %s\n", formatDur(time.Duration(s.Duration)))
	fmt.Fprintf(&b, "  status:   %s\n", statusBadge(s.Active))
	fmt.Fprintf(&b, "  tools:    %d    prompts: %d    files: %d\n\n", s.ToolCalls, s.Prompts, s.FilesChanged)
	return b.String()
}

func (m *model) renderSessionToolSummary(s *SessionDetail) string {
	if len(s.ToolSummary) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", sectionStyle.Render("▸ Tool Summary"))
	tools := s.ToolSummary
	if len(tools) > 10 {
		tools = tools[:10]
	}
	maxCalls := tools[0].CallCount
	interior := m.interiorWidth()
	// Fixed: 2(indent) + 12(tool) + 2 + bar + 1 + 4(Calls) + 2 + 6(Errors) + 2 + 8(Success%) + 2 + 8(Avg Dur)
	barW := interior - 49
	if barW < 3 {
		barW = 3
	}
	if barW > 20 {
		barW = 20
	}
	fmt.Fprintf(&b, "  %-12s  %-*s %4s  %6s  %8s  %8s\n", "Tool", barW, "Bar", "Calls", "Errors", "Success%", "Avg Dur")
	for _, t := range tools {
		bar := barChart(t.CallCount, maxCalls, barW)
		fmt.Fprintf(&b, "  %-12s  %s %4d  %6d  %7.1f%%  %8s\n",
			truncate(t.Tool, 12), barStyleOK.Render(bar), t.CallCount, t.ErrorCount,
			t.SuccessRate*100, formatDur(time.Duration(t.AvgDuration)))
	}
	b.WriteString("\n")
	return b.String()
}

func (m *model) renderSessionAssistantResponse(s *SessionDetail) string {
	if s.AssistantResponse == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", sectionStyle.Render("▸ Session Result"))
	interior := m.interiorWidth()
	// Wrap long lines to interior width
	words := strings.Fields(s.AssistantResponse)
	var line strings.Builder
	for _, w := range words {
		if line.Len() > 0 && line.Len()+1+len(w) > interior-2 {
			fmt.Fprintf(&b, "  %s\n", mutedStyle.Render(line.String()))
			line.Reset()
		}
		if line.Len() > 0 {
			line.WriteString(" ")
		}
		line.WriteString(w)
	}
	if line.Len() > 0 {
		fmt.Fprintf(&b, "  %s\n", mutedStyle.Render(line.String()))
	}
	b.WriteString("\n")
	return b.String()
}

func (m *model) renderSessionChanges(s *SessionDetail) string {
	if len(s.Changes) == 0 {
		return ""
	}

	// Group changes by path, preserving insertion order for sort.
	paths := make([]string, 0, s.FilesChanged)
	seen := map[string][]FileChange{}
	for _, fc := range s.Changes {
		if _, ok := seen[fc.Path]; !ok {
			paths = append(paths, fc.Path)
		}
		seen[fc.Path] = append(seen[fc.Path], fc)
	}

	// Sort by lastTs desc, ties by path asc.
	lastTs := map[string]time.Time{}
	for p, edits := range seen {
		lastTs[p] = edits[len(edits)-1].Ts
	}
	slices.SortFunc(paths, func(a, b string) int {
		if c := lastTs[b].Compare(lastTs[a]); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})

	nFiles := s.FilesChanged
	nEdits := len(s.Changes)
	fileWord := "files"
	if nFiles == 1 {
		fileWord = "file"
	}
	editWord := "edits"
	if nEdits == 1 {
		editWord = "edit"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", sectionStyle.Render(fmt.Sprintf("▸ Changes (%d %s, %d %s)", nFiles, fileWord, nEdits, editWord)))
	fmt.Fprintf(&b, "%s\n", mutedStyle.Render("  see Timeline for full event order"))
	if HasShellEvent(*s) {
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render("  ⚠ also ran shell — file changes via shell not shown"))
	}

	for _, p := range paths {
		edits := seen[p]
		shortPath := shortenPath(m.homeDir, p, s.Cwd)
		editCount := len(edits)
		editCountStr := fmt.Sprintf("%d edits", editCount)
		if editCount == 1 {
			editCountStr = "1 edit"
		}
		fileLastTs := lastTs[p].Local().Format("15:04:05")

		// Aggregate +/- counts across non-oversized edits.
		var totalAdds, totalDels, oversizedCount int
		for _, fc := range edits {
			if a, d, ok := DiffLineCounts(fc); ok {
				totalAdds += a
				totalDels += d
			} else if fc.Oversized {
				oversizedCount++
			}
		}

		// Build file line: path  +N/-M  K edits  last HH:MM:SS
		var fileCounts string
		if oversizedCount == len(edits) {
			// All oversized — no +/- badge.
			fileCounts = mutedStyle.Render("—")
		} else {
			fileCounts = addStyle.Render(fmt.Sprintf("+%d", totalAdds)) + "/" + delStyle.Render(fmt.Sprintf("-%d", totalDels))
		}
		metaStr := editCountStr + "  last " + fileLastTs
		if oversizedCount > 0 && oversizedCount < len(edits) {
			metaStr += "  " + mutedStyle.Render(fmt.Sprintf("(%d oversized)", oversizedCount))
		}

		lineWidth := 2 + len(shortPath) + 2 + len(editCountStr) + 2 + len("last 00:00:00")
		if lineWidth > m.interiorWidth() {
			fmt.Fprintf(&b, "  %s  %s\n    %s\n", shortPath, fileCounts, metaStr)
		} else {
			fmt.Fprintf(&b, "  %s  %s  %s\n", shortPath, fileCounts, metaStr)
		}

		for _, fc := range edits {
			var purpose string
			if fc.Purpose != "" {
				purpose = `"` + fc.Purpose + `"`
			} else {
				purpose = mutedStyle.Render("(no purpose)")
			}
			if fc.Oversized {
				fmt.Fprintf(&b, "    • [%s] %s %s\n",
					mutedStyle.Render(fc.Command), purpose,
					mutedStyle.Render("(oversized — diff unavailable)"))
				continue
			}
			// Per-edit +/- counts.
			var editCounts string
			if a, d, ok := DiffLineCounts(fc); ok {
				editCounts = " " + addStyle.Render(fmt.Sprintf("+%d", a)) + "/" + delStyle.Render(fmt.Sprintf("-%d", d))
			}
			fmt.Fprintf(&b, "    • [%s] %s%s\n", mutedStyle.Render(fc.Command), purpose, editCounts)
			// Diff preview (gated behind changesExpanded toggle).
			if m.changesExpanded {
				if preview := renderEditPreview(fc, 32); preview != "" {
					b.WriteString(preview)
				}
			}
		}
	}
	b.WriteString("\n")
	return b.String()
}

const previewIndent = "         " // 9 spaces

// renderEditPreview returns a styled diff preview for fc, capped at maxLines output lines.
// Returns empty string for oversized edits or when there is no diff.
func renderEditPreview(fc FileChange, maxLines int) string {
	if fc.Oversized {
		return ""
	}
	var old, new string
	switch fc.Command {
	case "create", "insert":
		old, new = "", fc.Content
	case "strReplace":
		old, new = fc.OldStr, fc.NewStr
	default:
		return ""
	}
	if old == "" && new == "" {
		return ""
	}

	raw := udiff.Unified("", "", old, new)
	if raw == "" {
		return ""
	}

	var out strings.Builder
	count := 0
	lines := strings.Split(raw, "\n")
	// Remove trailing empty line from Split.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	// Filter out --- and +++ file header lines; keep @@ and content lines.
	var filtered []string
	for _, l := range lines {
		if strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "+++ ") || strings.HasPrefix(l, `\ `) {
			continue
		}
		filtered = append(filtered, l)
	}

	total := len(filtered)
	for i, l := range filtered {
		if count >= maxLines {
			remaining := total - i
			out.WriteString(previewIndent)
			out.WriteString(mutedStyle.Render(fmt.Sprintf("…%d more lines", remaining)))
			out.WriteString("\n")
			break
		}
		var styled string
		if strings.HasPrefix(l, "+") {
			styled = addStyle.Render(l)
		} else if strings.HasPrefix(l, "-") {
			styled = delStyle.Render(l)
		} else if strings.HasPrefix(l, "@@") {
			styled = hunkStyle.Render(l)
		} else {
			styled = mutedStyle.Render(l)
		}
		out.WriteString(previewIndent)
		out.WriteString(styled)
		out.WriteString("\n")
		count++
	}
	return out.String()
}

func (m *model) renderSessionPrompts(s *SessionDetail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", sectionStyle.Render("▸ Prompts (newest first)"))
	if len(s.PromptHistory) == 0 {
		b.WriteString(mutedStyle.Render("  (none)\n"))
	} else {
		for i, p := range s.PromptHistory {
			text := singleLine(p)
			if !m.promptExpanded {
				text = truncate(text, 200)
			}
			fmt.Fprintf(&b, "  %d. %s\n", i+1, text)
		}
	}
	b.WriteString("\n")
	return b.String()
}

func (m *model) renderSessionTimeline(s *SessionDetail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", sectionStyle.Render("▸ Timeline"))
	// Fixed columns: 2(indent) + 1(marker) + 1 + 8(time) + 2 + 12(label) + 2 + 7(dur) + 2 = 37
	const tlFixed = 37
	summaryW := m.interiorWidth() - tlFixed
	if summaryW < 10 {
		summaryW = 10
	}
	for _, e := range s.Timeline {
		// Skip postToolUse unless it's an error.
		if e.Event == apmconfig.EventPostToolUse && !e.IsError {
			continue
		}
		marker := " "
		if e.IsError {
			marker = errorStyle.Render("!")
		}
		label := timelineLabel(e)
		ts := e.Ts.Local().Format("15:04:05")
		var dur string
		if time.Duration(e.Duration) > 0 {
			dur = fmt.Sprintf("[%s]", formatDur(time.Duration(e.Duration)))
		}
		summary := shortenPath(m.homeDir, e.InputSummary, s.Cwd)
		fmt.Fprintf(&b, "  %s %s  %-12s  %7s  %s\n",
			marker, ts, label, dur,
			mutedStyle.Render(truncateVisible(summary, summaryW)))
		if e.IsError && e.ErrorDetail != "" {
			detail := truncateVisible(e.ErrorDetail, m.interiorWidth()-4)
			fmt.Fprintf(&b, "    %s\n", errorStyle.Render(detail))
		}
	}
	return b.String()
}

// timelineLabel returns a concise label for a timeline event.
// preToolUse → tool name, other events → short human name.
func timelineLabel(e EventEntry) string {
	switch e.Event {
	case apmconfig.EventPreToolUse:
		if e.Tool != "" {
			return e.Tool
		}
		return "tool"
	case apmconfig.EventPostToolUse:
		if e.Tool != "" {
			return e.Tool + " err"
		}
		return "tool err"
	case apmconfig.EventAgentSpawn:
		return "spawn"
	case apmconfig.EventUserPromptSubmit:
		return "prompt"
	case apmconfig.EventStop:
		return "stop"
	}
	return e.Event
}

// shortenPath applies project-relative and home-directory abbreviation to paths
// found in an InputSummary string.
func shortenPath(home, s, cwd string) string {
	if s == "" {
		return s
	}
	if cwd != "" {
		if suffix, ok := trimPathPrefix(s, cwd); ok {
			return suffix
		}
	}
	return abbrevHome(home, s)
}

// --- Agents list ------------------------------------------------------------

func (m *model) renderAgentsList() string {
	agents := m.metrics.Agents
	interior := m.interiorWidth()

	// Fixed chars: 2(indent) + 2 + 8(Sessions) + 2 + 7(Tools) + 2 + 8(Prompts) + 2 + 8(Errors) = 41
	fixed := 2 + 2 + 8 + 2 + 7 + 2 + 8 + 2 + 8
	nameW := interior - fixed
	if nameW < 16 {
		nameW = 16
	}

	var b strings.Builder
	fmt.Fprintf(&b, "  %-*s  %8s  %7s  %8s  %8s\n",
		nameW, "Name", "Sessions", "Tools", "Prompts", "Errors")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", interior)))
	b.WriteString("\n")

	rows := m.viewportHeight()
	start := clampOffset(m.cursor[tabAgents], len(agents), rows)
	end := start + rows
	if end > len(agents) {
		end = len(agents)
	}
	for i := start; i < end; i++ {
		a := agents[i]
		row := fmt.Sprintf("  %-*s  %8d  %7d  %8d  %8d",
			nameW, truncate(a.Name, nameW), a.SessionCount, a.ToolCalls, a.Prompts, a.ToolErrorCnt)
		if i == m.cursor[tabAgents] {
			b.WriteString(selectedStyle.Render("▸ " + row[2:]))
		} else {
			b.WriteString(row)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n%s  %d/%d", mutedStyle.Render("showing"), end-start, len(agents))
	return borderStyle.Width(m.contentWidth()).Render(b.String())
}

// --- Agent detail -----------------------------------------------------------

func (m *model) renderAgentDetail() string {
	agents := m.metrics.Agents
	if len(agents) == 0 {
		return ""
	}
	idx := m.cursor[tabAgents]
	if idx >= len(agents) {
		idx = len(agents) - 1
	}
	a := agents[idx]

	var errRate float64
	if a.ToolCalls > 0 {
		errRate = float64(a.ToolErrorCnt) / float64(a.ToolCalls)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", sectionStyle.Render("Agent"), a.Name)
	fmt.Fprintf(&b, "  sessions: %d    tool calls: %d    prompts: %d\n",
		a.SessionCount, a.ToolCalls, a.Prompts)
	fmt.Fprintf(&b, "  errors:   %d  (%s)\n\n",
		a.ToolErrorCnt, formatErrRate(errRate))

	fmt.Fprintf(&b, "%s\n", sectionStyle.Render("▸ Top tools used"))
	tcs := a.ToolSummary
	if len(tcs) == 0 {
		b.WriteString(mutedStyle.Render("  (none)\n"))
	} else {
		limit := 10
		if len(tcs) < limit {
			limit = len(tcs)
		}
		maxC := tcs[0].CallCount
		fmt.Fprintf(&b, "  %-14s  %-20s %5s  %6s  %8s  %8s\n",
			"Tool", "", "Calls", "Errors", "Success%", "Avg Dur")
		for i := range limit {
			t := tcs[i]
			fmt.Fprintf(&b, "  %-14s  %s %5d  %6d  %7.0f%%  %8s\n",
				truncate(t.Tool, 14), barStyleOK.Render(barChart(t.CallCount, maxC, 20)),
				t.CallCount, t.ErrorCount, t.SuccessRate*100, formatDur(time.Duration(t.AvgDuration)))
		}
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "%s\n", sectionStyle.Render("▸ Sessions (newest first)"))
	fmt.Fprintf(&b, "  %-12s  %-16s  %-10s  %-9s  %6s  %7s  %s\n",
		"ID", "Started", "Duration", "Status", "Tools", "Prompts", "Title")
	limit := 15
	if len(a.Sessions) < limit {
		limit = len(a.Sessions)
	}
	for i := range limit {
		s := a.Sessions[i]
		fmt.Fprintf(&b, "  %-12s  %-16s  %-10s  %s  %6d  %7d  %s\n",
			shortID(s.ID, 12),
			s.StartTime.Local().Format("2006-01-02 15:04"),
			formatDur(time.Duration(s.Duration)),
			padRightVisible(statusBadge(s.Active), 9),
			s.ToolCalls, s.Prompts,
			truncateVisible(cmp.Or(s.Title, "—"), 40),
		)
	}
	if len(a.Sessions) > limit {
		fmt.Fprintf(&b, "  %s\n", mutedStyle.Render(fmt.Sprintf("… and %d more", len(a.Sessions)-limit)))
	}
	return b.String()
}

// --- Tools list -------------------------------------------------------------

func (m *model) renderToolsList() string {
	tools := m.metrics.Tools
	interior := m.interiorWidth()

	// Fixed chars: 2(indent) + 2 + 7(Calls) + 2 + 7(Errors) + 2 + 7(Err%) + 2 + 10(Avg dur) = 41
	const baseFixed = 41
	barW := min(20, interior-baseFixed-2-16)
	showBar := barW >= 3
	fixed := baseFixed
	if showBar {
		fixed = baseFixed + 2 + barW
	}
	nameW := interior - fixed
	if nameW < 16 {
		nameW = 16
	}

	var maxCall int
	if len(tools) > 0 {
		maxCall = tools[0].CallCount
	}

	var b strings.Builder
	if showBar {
		fmt.Fprintf(&b, "  %-*s  %7s  %-*s  %7s  %7s  %10s\n",
			nameW, "Name", "Calls", barW, "Bar", "Errors", "Err%", "Avg dur")
	} else {
		fmt.Fprintf(&b, "  %-*s  %7s  %7s  %7s  %10s\n",
			nameW, "Name", "Calls", "Errors", "Err%", "Avg dur")
	}
	b.WriteString(mutedStyle.Render(strings.Repeat("─", interior)))
	b.WriteString("\n")

	rows := m.viewportHeight()
	start := clampOffset(m.cursor[tabTools], len(tools), rows)
	end := start + rows
	if end > len(tools) {
		end = len(tools)
	}
	for i := start; i < end; i++ {
		t := tools[i]
		var row string
		if showBar {
			bar := ""
			if maxCall > 0 {
				bar = barChart(t.CallCount, maxCall, barW)
			}
			row = fmt.Sprintf("  %-*s  %7d  %-*s  %7d  %7s  %10s",
				nameW, truncate(t.Name, nameW), t.CallCount, barW, bar,
				t.ErrorCount, formatErrRate(t.ErrorRate), formatDur(time.Duration(t.AvgDuration)))
		} else {
			row = fmt.Sprintf("  %-*s  %7d  %7d  %7s  %10s",
				nameW, truncate(t.Name, nameW), t.CallCount, t.ErrorCount,
				formatErrRate(t.ErrorRate), formatDur(time.Duration(t.AvgDuration)))
		}
		if i == m.cursor[tabTools] {
			b.WriteString(selectedStyle.Render("▸ " + row[2:]))
		} else {
			b.WriteString(row)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n%s  %d/%d", mutedStyle.Render("showing"), end-start, len(tools))
	return borderStyle.Width(m.contentWidth()).Render(b.String())
}

// --- Tool detail ------------------------------------------------------------

func (m *model) renderToolDetail() string {
	tools := m.metrics.Tools
	if len(tools) == 0 {
		return ""
	}
	idx := m.cursor[tabTools]
	if idx >= len(tools) {
		idx = len(tools) - 1
	}
	t := tools[idx]

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", sectionStyle.Render("Tool"), t.Name)
	fmt.Fprintf(&b, "  calls:    %d    errors: %d    rate: %s\n",
		t.CallCount, t.ErrorCount, formatErrRate(t.ErrorRate))
	fmt.Fprintf(&b, "  avg duration: %s\n\n", formatDur(time.Duration(t.AvgDuration)))

	fmt.Fprintf(&b, "%s\n", sectionStyle.Render("▸ Recent calls (newest first)"))
	if len(t.RecentCalls) == 0 {
		b.WriteString(mutedStyle.Render("  (none)\n"))
	} else {
		fmt.Fprintf(&b, "  %-19s  %-12s  %-14s  %10s\n", "Time", "Session", "Agent", "Duration")
		limit := 15
		if len(t.RecentCalls) < limit {
			limit = len(t.RecentCalls)
		}
		for i := range limit {
			c := t.RecentCalls[i]
			fmt.Fprintf(&b, "  %-19s  %-12s  %-14s  %10s\n",
				c.Ts.Local().Format(tsLayout),
				shortID(c.Session, 12),
				truncate(c.Agent, 14),
				formatDur(time.Duration(c.Duration)),
			)
		}
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "%s\n", sectionStyle.Render("▸ Error samples"))
	if len(t.Errors) == 0 {
		b.WriteString(mutedStyle.Render("  (none)\n"))
	} else {
		fmt.Fprintf(&b, "  %-19s  %-12s  %-14s  %s\n", "Time", "Session", "Agent", "Input")
		limit := 10
		if len(t.Errors) < limit {
			limit = len(t.Errors)
		}
		// Reserve space: 2(indent) + 2(!+space) + 19(time) + 2 + 12(session) + 2 + 14(agent) + 2 = 55
		inputWidth := m.interiorWidth() - 55
		if inputWidth < 10 {
			inputWidth = 10
		}
		for i := range limit {
			c := t.Errors[i]
			fmt.Fprintf(&b, "  %s %-19s  %-12s  %-14s  %s\n",
				errorStyle.Render("!"),
				c.Ts.Local().Format(tsLayout),
				shortID(c.Session, 12),
				truncate(c.Agent, 14),
				truncateVisible(c.InputSummary, inputWidth),
			)
		}
	}
	return b.String()
}

// truncateVisible truncates s so lipgloss.Width(s) <= n, preserving simple ASCII content.
func truncateVisible(s string, n int) string {
	if n <= 0 || lipgloss.Width(s) <= n {
		return s
	}
	// Walk runes building output until we reach n-1 visible cells, then append '…'.
	runes := []rune(s)
	if n == 1 {
		return "…"
	}
	var sb strings.Builder
	var w int
	for _, r := range runes {
		rw := lipgloss.Width(string(r))
		if w+rw > n-1 {
			break
		}
		sb.WriteRune(r)
		w += rw
	}
	sb.WriteRune('…')
	return sb.String()
}

// --- utilities --------------------------------------------------------------

func formatErrRate(r float64) string {
	pct := r * 100
	switch {
	case pct >= 10:
		return errorStyle.Render(fmt.Sprintf("%5.1f%%", pct))
	case pct > 0:
		return warnStyle.Render(fmt.Sprintf("%5.1f%%", pct))
	}
	return mutedStyle.Render("  0.0%")
}

func (m *model) contentWidth() int {
	// The block width passed to borderStyle.Width(...). Border + padding consume
	// 4 cells, and we want the block to fit within (terminal width - 4).
	w := m.width - 4
	if w < 60 {
		w = 60
	}
	if w > 200 {
		w = 200
	}
	return w
}

// interiorWidth is the usable content width inside contentWidth() (minus border+padding).
func (m *model) interiorWidth() int {
	w := m.contentWidth() - 4
	if w < 40 {
		w = 40
	}
	return w
}

// clampOffset returns the list offset so the cursor is visible within a window of `rows`.
func clampOffset(cursor, total, rows int) int {
	if total <= rows || cursor < rows {
		return 0
	}
	start := cursor - rows + 1
	if start+rows > total {
		start = total - rows
	}
	if start < 0 {
		start = 0
	}
	return start
}

// --- Skills tab -------------------------------------------------------------

func (m *model) renderSkillsTab() string {
	skills := m.metrics.Skills
	interior := m.interiorWidth()
	// Fixed: 2(indent) + 1 + 8(Reads) = 11 + name
	nameW := interior - 11
	if nameW < 20 {
		nameW = 20
	}

	var b strings.Builder
	fmt.Fprintf(&b, "  %-*s %8s\n", nameW, "Name", "Reads")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", interior)))
	b.WriteString("\n")
	if len(skills) == 0 {
		b.WriteString(mutedStyle.Render("  no skill reads"))
		return borderStyle.Width(m.contentWidth()).Render(b.String())
	}
	rows := m.viewportHeight()
	start := clampOffset(m.cursor[tabSkills], len(skills), rows)
	end := start + rows
	if end > len(skills) {
		end = len(skills)
	}
	for i := start; i < end; i++ {
		sk := skills[i]
		row := fmt.Sprintf("  %-*s %8d", nameW, truncate(sk.Name, nameW), sk.ReadCount)
		if i == m.cursor[tabSkills] {
			b.WriteString(selectedStyle.Render("▸ " + row[2:]))
		} else {
			b.WriteString(row)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n%s  %d/%d", mutedStyle.Render("showing"), end-start, len(skills))
	return borderStyle.Width(m.contentWidth()).Render(b.String())
}

func (m *model) renderSkillDetail() string {
	skills := m.metrics.Skills
	if len(skills) == 0 {
		return ""
	}
	idx := m.cursor[tabSkills]
	if idx >= len(skills) {
		idx = len(skills) - 1
	}
	sk := skills[idx]

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", sectionStyle.Render("Skill"), sk.Name)
	fmt.Fprintf(&b, "  reads: %d\n\n", sk.ReadCount)
	b.WriteString(mutedStyle.Render("  (SKILL.md read count across sessions in the selected window)"))
	return b.String()
}
