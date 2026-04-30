package monitor

import (
	"cmp"
	"fmt"
	"strings"
	"time"
)

func (m *model) renderAgentsList() string {
	agents := m.metrics.Agents
	interior := m.interiorWidth()

	// Fixed chars: 2(indent) + 2 + 8(Sessions) + 2 + 7(Tools) + 2 + 8(Prompts) + 2 + 8(Errors) = 41
	fixed := 2 + 2 + 8 + 2 + 7 + 2 + 8 + 2 + 8
	nameW := max(interior-fixed, 16)

	var b strings.Builder
	fmt.Fprintf(&b, "  %-*s  %8s  %7s  %8s  %8s\n",
		nameW, "Name", "Sessions", "Tools", "Prompts", "Errors")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", interior)))
	b.WriteString("\n")

	rows := m.viewportHeight()
	start := clampOffset(m.cursor[tabAgents], len(agents), rows)
	end := min(start+rows, len(agents))
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
		limit := min(len(tcs), 10)
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
	limit := min(len(a.Sessions), 15)
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
