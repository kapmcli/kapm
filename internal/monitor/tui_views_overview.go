package monitor

import (
	"cmp"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

const (
	colWidthRecentID      = 12
	colWidthRecentDur     = 8
	colWidthRecentStatus  = 9
	colWidthRecentTools   = 5
	colWidthRecentPrompts = 7
	colWidthRecentCredits = 7
	colWidthRecentLastAct = 11
	colWidthRecentTitle   = 40
)

var recentSessionsFixed = sumColWidths(2, 2,
	colWidthRecentID, colWidthRecentDur, colWidthRecentStatus,
	colWidthRecentTools, colWidthRecentPrompts, colWidthRecentCredits,
	colWidthRecentLastAct)

var activityBars = [...]rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// renderOverview renders the overview tab.
func (m *model) renderOverview() string {
	p := m.overviewLayout()
	full := m.contentWidth()

	// Row 1: Summary (+ Activity if showActivity and non-empty) side-by-side.
	var row1 string
	if p.showActivity && len(m.metrics.Overview.HourlyActivity) > 0 {
		ws := splitBoxWidths(full, 2, 1)
		row1 = lipgloss.JoinHorizontal(lipgloss.Top,
			m.renderSummaryBox(ws[0]), " ", m.renderActivityBox(ws[1]))
	} else {
		row1 = m.renderSummaryBox(full)
	}

	// Row 2: Top tools + Top agents (+ Skills) based on columns param.
	var parts []string
	if p.showRow2 {
		hasSkills := len(m.metrics.Skills) > 0
		effectiveCols := p.columns
		if effectiveCols == 3 && !hasSkills {
			effectiveCols = 2
		}
		switch effectiveCols {
		case 1:
			parts = append(parts,
				m.renderTopTools(p.topN, full),
				m.renderTopAgents(p.topN, full),
			)
			if hasSkills {
				parts = append(parts, m.renderTopSkills(p.topN, full))
			}
		case 2:
			ws := splitBoxWidths(full, 2, 1)
			parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top,
				m.renderTopTools(p.topN, ws[0]), " ", m.renderTopAgents(p.topN, ws[1])))
		default: // 3
			ws := splitBoxWidths(full, 3, 1)
			parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top,
				m.renderTopTools(p.topN, ws[0]), " ",
				m.renderTopAgents(p.topN, ws[1]), " ",
				m.renderTopSkills(p.topN, ws[2])))
		}
	}

	// Row 3: Recent active sessions full width.
	row3 := m.renderRecentSessionsBox(full, p.recentN)

	result := row1
	if len(parts) > 0 {
		result += "\n" + strings.Join(parts, "\n")
	}
	return result + "\n" + row3
}

// renderSummaryBox draws the Summary panel with logs dir + counters, fitted to width.
func (m *model) renderSummaryBox(width int) string {
	ov := m.metrics.Overview

	interior := interiorOf(width)
	logsDir := abbrevHome(m.homeDir, m.hookLogsDir)
	logsDir = filepath.ToSlash(logsDir)
	// Reserve 12 chars for "logs dir:  " prefix.
	if n := interior - len("logs dir: "); n > 0 && len(logsDir) > n {
		logsDir = truncateLeft(logsDir, n)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", sectionStyle.Render("▸ Summary"))
	fmt.Fprintf(&b, "  logs dir: %s\n", mutedStyle.Render(logsDir))
	fmt.Fprintf(&b, "  sessions:   %-5d  active: %s\n",
		len(ov.Sessions), activeStyle.Render(fmt.Sprintf("%d", m.activeSessions)))
	fmt.Fprintf(&b, "  agents:     %-5d  errors: %s\n",
		len(ov.Agents), errorCountText(m.totalErrors))
	fmt.Fprintf(&b, "  tool calls: %-5d  prompts: %d",
		m.totalTools, m.totalPrompts)
	if m.kiroUsage != nil {
		fmt.Fprintf(&b, "\n  kiro usage: %s", m.kiroUsage.CreditLabel())
		fmt.Fprintf(&b, "\n  usage:      %s · resets %s", m.kiroUsage.PercentLabel(), m.kiroUsage.ResetDate)
		fmt.Fprintf(&b, "\n  plan:       %s", m.kiroUsage.MetaLabel())
	} else if m.kiroUsageRead != nil {
		status := "checking…"
		if !m.kiroUsageFetchedAt.IsZero() {
			status = "unavailable"
		}
		fmt.Fprintf(&b, "\n  kiro usage: %s", mutedStyle.Render(status))
	}
	return borderStyle.Width(width).Render(b.String())
}

// renderTopBox renders a bordered panel with a section title and up to `limit`
// rows. The caller supplies a row renderer that receives the interior width
// and writes row content to the supplied *strings.Builder. empty(b) is called
// when there are no rows to render.
func (m *model) renderTopBox(
	title string,
	width, limit int,
	haveData bool,
	empty func(b *strings.Builder),
	rows func(b *strings.Builder, interior int),
) string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("▸ " + title))
	b.WriteString("\n")
	if !haveData {
		empty(&b)
		return borderStyle.Width(width).Render(b.String())
	}
	interior := interiorOf(width)
	rows(&b, interior)
	_ = limit // caller's rows closure caps itself; passed for optional use
	return borderStyle.Width(width).Render(b.String())
}

func (m *model) renderTopTools(limit, width int) string {
	tools := m.metrics.Overview.Tools
	return m.renderTopBox("Top tools", width, limit, len(tools) > 0,
		func(b *strings.Builder) {
			b.WriteString(mutedStyle.Render("  no tool usage"))
		},
		func(b *strings.Builder, interior int) {
			if len(tools) < limit {
				limit = len(tools)
			}
			// Reserve: 2(indent) + 12(name) + 1 + bar + 1 + 4(count) + 5(" err:") + 6(err%) = 31 + bar
			barW := min(max(interior-31, 3), 20)
			maxCall := tools[0].CallCount
			for i := range limit {
				t := tools[i]
				bar := barChart(t.CallCount, maxCall, barW)
				fmt.Fprintf(b, "  %-12s %s %4d err:%s\n",
					truncate(t.Name, 12), barStyleOK.Render(bar), t.CallCount, formatErrRate(t.ErrorRate))
			}
		})
}

func (m *model) renderTopAgents(limit, width int) string {
	agents := m.metrics.Overview.Agents
	return m.renderTopBox("Top agents", width, limit, len(agents) > 0,
		func(b *strings.Builder) {
			b.WriteString(mutedStyle.Render("  no agent activity"))
		},
		func(b *strings.Builder, interior int) {
			if len(agents) < limit {
				limit = len(agents)
			}
			// Reserve 2(indent) + 1 + 6(Sess) + 1 + 5(Tool) + 1 + 6(Prompt) = 22
			nameW := min(max(interior-22, 8), 30)
			fmt.Fprintf(b, "  %-*s %6s %5s %6s\n", nameW, "Name", "Sess", "Tools", "Prompt")
			for i := range limit {
				a := agents[i]
				fmt.Fprintf(b, "  %-*s %6d %5d %6d\n",
					nameW, truncate(a.Name, nameW), a.SessionCount, a.ToolCalls, a.Prompts)
			}
		})
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
	maxCols := max(interior/2, 1)
	if len(hours) > maxCols {
		hours = hours[len(hours)-maxCols:]
	}

	bars := activityBars[:]
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
	skills := m.metrics.Skills
	return m.renderTopBox("Skills (reads)", width, limit, len(skills) > 0,
		func(b *strings.Builder) {
			b.WriteString(mutedStyle.Render("  no skill reads"))
		},
		func(b *strings.Builder, interior int) {
			if len(skills) < limit {
				limit = len(skills)
			}
			maxC := skills[0].ReadCount
			// Reserve 2(indent) + 18(name) + 1 + bar + 1 + 4(count) = 26 + bar
			barW := min(max(interior-26, 3), 20)
			for i := range limit {
				sk := skills[i]
				bar := barChart(sk.ReadCount, maxC, barW)
				fmt.Fprintf(b, "  %-18s %s %4d\n",
					truncate(sk.Name, 18), barStyleOK.Render(bar), sk.ReadCount)
			}
		})
}

// renderRecentSessionsBox renders a full-width box showing top N sessions
// (already sorted by LastActivity desc) with a detailed row per session.
func (m *model) renderRecentSessionsBox(width, limit int) string {
	sessions := m.metrics.Sessions
	if len(sessions) > limit {
		sessions = sessions[:limit]
	}
	interior := interiorOf(width)

	prefix := sectionStyle.Render("▸ Recent active sessions") + "\n"

	if len(sessions) == 0 {
		var b strings.Builder
		b.WriteString(prefix)
		b.WriteString(mutedStyle.Render("  (none)"))
		return borderStyle.Width(width).Render(b.String())
	}

	remaining := interior - recentSessionsFixed - 2 // 2 spaces between ID and Agent
	agentW := min(max(remaining-2-colWidthRecentTitle, 10), 16)

	cols := []Column{
		{Header: "ID", Width: colWidthRecentID},
		{Header: "Agent", Width: agentW},
		{Header: "Title", Width: colWidthRecentTitle},
		{Header: "Duration", Width: colWidthRecentDur},
		{Header: "Status", Width: colWidthRecentStatus},
		{Header: "Tools", Width: colWidthRecentTools, Right: true},
		{Header: "Prompts", Width: colWidthRecentPrompts, Right: true},
		{Header: "Credits", Width: colWidthRecentCredits, Right: true},
		{Header: "Last act", Width: colWidthRecentLastAct},
	}

	now := time.Now()
	var prevID string
	rows := make([][]string, len(sessions))
	for i, s := range sessions {
		idCell := shortID(s.ID, colWidthRecentID)
		if s.ID == prevID {
			idCell = blankID
		}
		prevID = s.ID
		rows[i] = []string{
			fmt.Sprintf("%-*s", colWidthRecentID, idCell),
			fmt.Sprintf("%-*s", agentW, truncate(s.Agent, agentW)),
			padRightVisible(truncateVisible(cmp.Or(s.Title, "—"), colWidthRecentTitle), colWidthRecentTitle),
			fmt.Sprintf("%-*s", colWidthRecentDur, formatDur(time.Duration(s.Duration))),
			padRightVisible(statusBadge(s.Active), colWidthRecentStatus),
			fmt.Sprintf("%*d", colWidthRecentTools, s.ToolCalls),
			fmt.Sprintf("%*d", colWidthRecentPrompts, s.Prompts),
			fmt.Sprintf("%*s", colWidthRecentCredits, formatCredits(s.TotalCredits)),
			fmt.Sprintf("%-*s", colWidthRecentLastAct, formatLastActivity(s.LastActivity, now)),
		}
	}

	return m.renderListView(listViewOpts{
		columns: cols,
		rows:    rows,
		cursor:  -1,
		gap:     2,
		width:   width,
		prefix:  prefix,
	})
}

