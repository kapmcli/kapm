package monitor

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
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
	end := min(start+rows, len(tools))
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

func (m *model) renderSkillsTab() string {
	skills := m.metrics.Skills
	interior := m.interiorWidth()
	// Fixed: 2(indent) + 1 + 8(Reads) = 11 + name
	nameW := max(interior-11, 20)

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
	end := min(start+rows, len(skills))
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
