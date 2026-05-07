package monitor

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
)

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
	nameW := max(interior-fixed, 16)

	var maxCall int
	if len(tools) > 0 {
		maxCall = tools[0].CallCount
	}

	var cols []Column
	if showBar {
		cols = []Column{
			{Header: "Name", Width: nameW},
			{Header: "Calls", Width: 7, Right: true},
			{Header: "Bar", Width: barW},
			{Header: "Errors", Width: 7, Right: true},
			{Header: "Err%", Width: 7, Right: true},
			{Header: "Avg dur", Width: 10, Right: true},
		}
	} else {
		cols = []Column{
			{Header: "Name", Width: nameW},
			{Header: "Calls", Width: 7, Right: true},
			{Header: "Errors", Width: 7, Right: true},
			{Header: "Err%", Width: 7, Right: true},
			{Header: "Avg dur", Width: 10, Right: true},
		}
	}

	rows := make([][]string, len(tools))
	for i, t := range tools {
		if showBar {
			bar := ""
			if maxCall > 0 {
				bar = barChart(t.CallCount, maxCall, barW)
			}
			rows[i] = []string{
				fmt.Sprintf("%-*s", nameW, truncate(t.Name, nameW)),
				fmt.Sprintf("%7d", t.CallCount),
				fmt.Sprintf("%-*s", barW, bar),
				fmt.Sprintf("%7d", t.ErrorCount),
				fmt.Sprintf("%7s", formatErrRate(t.ErrorRate)),
				fmt.Sprintf("%10s", formatDur(time.Duration(t.AvgDuration))),
			}
		} else {
			rows[i] = []string{
				fmt.Sprintf("%-*s", nameW, truncate(t.Name, nameW)),
				fmt.Sprintf("%7d", t.CallCount),
				fmt.Sprintf("%7d", t.ErrorCount),
				fmt.Sprintf("%7s", formatErrRate(t.ErrorRate)),
				fmt.Sprintf("%10s", formatDur(time.Duration(t.AvgDuration))),
			}
		}
	}

	return m.renderListView(listViewOpts{
		columns: cols,
		rows:    rows,
		cursor:  m.cursor[tabTools],
	})
}

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

	if len(t.Aliases) > 1 {
		fmt.Fprintf(&b, "%s\n", sectionStyle.Render("▸ Aliases"))
		fmt.Fprintf(&b, "  %-16s  %7s  %7s  %7s\n", "Name", "Calls", "Errors", "Share")
		for _, a := range t.Aliases {
			fmt.Fprintf(&b, "  %-16s  %7d  %7d  %6.1f%%\n",
				truncate(a.Name, 16), a.CallCount, a.ErrorCount, a.Percentage*100)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "%s\n", sectionStyle.Render("▸ Recent calls (newest first)"))
	if len(t.RecentCalls) == 0 {
		b.WriteString(mutedStyle.Render("  (none)\n"))
	} else {
		fmt.Fprintf(&b, "  %-19s  %-12s  %-12s  %-14s  %10s\n", "Time", "Tool", "Session", "Agent", "Duration")
		limit := min(len(t.RecentCalls), 15)
		for i := range limit {
			c := t.RecentCalls[i]
			fmt.Fprintf(&b, "  %-19s  %-12s  %-12s  %-14s  %10s\n",
				c.Ts.Local().Format(tsLayout),
				truncate(c.Tool, 12),
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
		fmt.Fprintf(&b, "  %-19s  %-12s  %-12s  %-14s  %s\n", "Time", "Tool", "Session", "Agent", "Input")
		limit := min(len(t.Errors), 10)
		// Reserve space: 2(indent) + 2(!+space) + 19(time) + 2 + 12(tool) + 2 + 12(session) + 2 + 14(agent) + 2 = 69
		inputWidth := max(m.interiorWidth()-69, 10)
		for i := range limit {
			c := t.Errors[i]
			fmt.Fprintf(&b, "  %s %-19s  %-12s  %-12s  %-14s  %s\n",
				errorStyle.Render("!"),
				c.Ts.Local().Format(tsLayout),
				truncate(c.Tool, 12),
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
	if n <= 0 {
		return s
	}
	// ASCII fast path: when every byte is one visible cell, len == visible width.
	if len(s) <= n {
		return s
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	// Walk runes building output until we reach n-1 visible cells, then append '…'.
	if n == 1 {
		return "…"
	}
	var sb strings.Builder
	var w int
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > n-1 {
			break
		}
		sb.WriteRune(r)
		w += rw
	}
	sb.WriteRune('…')
	return sb.String()
}

func (m *model) renderSkillsTab() string {
	skills := m.metrics.Skills
	interior := m.interiorWidth()
	// Fixed: 2(indent) + 1 + 8(Reads) = 11 + name
	nameW := max(interior-11, 20)

	if len(skills) == 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "  %-*s %8s\n", nameW, "Name", "Reads")
		b.WriteString(mutedStyle.Render(strings.Repeat("─", interior)))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("  no skill reads"))
		return borderStyle.Width(m.contentWidth()).Render(b.String())
	}

	cols := []Column{
		{Header: "Name", Width: nameW},
		{Header: "Reads", Width: 8, Right: true},
	}

	rows := make([][]string, len(skills))
	for i, sk := range skills {
		rows[i] = []string{
			fmt.Sprintf("%-*s", nameW, truncate(sk.Name, nameW)),
			fmt.Sprintf("%8d", sk.ReadCount),
		}
	}

	return m.renderListView(listViewOpts{
		columns: cols,
		rows:    rows,
		cursor:  m.cursor[tabSkills],
		gap:     1,
	})
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
