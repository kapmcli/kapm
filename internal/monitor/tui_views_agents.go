package monitor

import (
	"cmp"
	"fmt"
	"strings"
	"time"
)

const (
	colWidthAgentSessions = 8
	colWidthAgentTools    = 7
	colWidthAgentPrompts  = 8
	colWidthAgentErrors   = 8
)

func (m *model) renderAgentsList() string {
	agents := m.metrics.Agents
	interior := m.interiorWidth()

	// Fixed chars: 2(indent) + 2 + colWidthAgentSessions + 2 + colWidthAgentTools + 2 + colWidthAgentPrompts + 2 + colWidthAgentErrors
	fixed := 2 + 2 + colWidthAgentSessions + 2 + colWidthAgentTools + 2 + colWidthAgentPrompts + 2 + colWidthAgentErrors
	nameW := max(interior-fixed, 16)

	cols := []Column{
		{Header: "Name", Width: nameW},
		{Header: "Sessions", Width: colWidthAgentSessions, Right: true},
		{Header: "Tools", Width: colWidthAgentTools, Right: true},
		{Header: "Prompts", Width: colWidthAgentPrompts, Right: true},
		{Header: "Errors", Width: colWidthAgentErrors, Right: true},
	}

	rows := make([][]string, len(agents))
	for i, a := range agents {
		rows[i] = []string{
			fmt.Sprintf("%-*s", nameW, truncate(a.Name, nameW)),
			fmt.Sprintf("%*d", colWidthAgentSessions, a.SessionCount),
			fmt.Sprintf("%*d", colWidthAgentTools, a.ToolCalls),
			fmt.Sprintf("%*d", colWidthAgentPrompts, a.Prompts),
			fmt.Sprintf("%*d", colWidthAgentErrors, a.ToolErrorCnt),
		}
	}

	return m.renderListView(listViewOpts{
		columns: cols,
		rows:    rows,
		cursor:  m.cursor[tabAgents],
	})
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
