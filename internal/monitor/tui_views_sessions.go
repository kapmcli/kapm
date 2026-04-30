package monitor

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
	"time"

	udiff "github.com/aymanbagabas/go-udiff"
	"github.com/kapmcli/kapm/internal/apmconfig"
)

func (m *model) renderSessionsList() string {
	sessions := m.metrics.Sessions
	interior := m.interiorWidth()

	// Fixed: 2(indent) + 12(ID) + 1 + agent + 1 + title + 1 + 8(Dur) + 1 + 9(Status) + 1 + 4(Tool) + 1 + 5(Prompt) + 1 + 5(Files) + 1 + 7(Credits) + 1 + 11(Last act)
	titleW := 40
	fixed := 2 + 12 + 1 + 1 + titleW + 1 + 8 + 1 + 9 + 1 + 4 + 1 + 5 + 1 + 5 + 1 + 7 + 1 + 11
	agentW := min(max(interior-fixed, 10), 16)

	var b strings.Builder
	fmt.Fprintf(&b, "  %-12s %-*s %-*s %-8s %-9s %4s %5s %5s %7s %-11s\n",
		"ID", agentW, "Agent", titleW, "Title", "Dur", "Status", "Tool", "Prompt", "Files", "Credits", "Last act")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", interior)))
	b.WriteString("\n")

	rows := m.viewportHeight()
	start := clampOffset(m.cursor[tabSessions], len(sessions), rows)
	end := min(start+rows, len(sessions))
	now := time.Now()
	var prevID string
	for i := start; i < end; i++ {
		s := sessions[i]
		idCell := shortID(s.ID, 12)
		agentCell := truncate(s.Agent, agentW)
		if s.ID == prevID {
			idCell = strings.Repeat(" ", 12)
		}
		prevID = s.ID
		titleCell := truncateVisible(cmp.Or(s.Title, "—"), titleW)
		status := statusBadge(s.Active)
		row := fmt.Sprintf("  %-12s %-*s %s %-8s %s %4d %5d %5d %7s %-11s",
			idCell,
			agentW, agentCell,
			padRightVisible(titleCell, titleW),
			formatDur(time.Duration(s.Duration)),
			padRightVisible(status, 9),
			s.ToolCalls, s.Prompts, s.FilesChanged,
			formatCredits(s.TotalCredits),
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

func formatCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return strconv.Itoa(n)
	}
}

func formatCredits(v float64) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.2f", v)
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
		m.renderSessionSubAgentCalls(s) +
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
	fmt.Fprintf(&b, "  tools:    %d    prompts: %d    files: %d", s.ToolCalls, s.Prompts, s.FilesChanged)
	if s.TotalCredits > 0 {
		fmt.Fprintf(&b, "    credits: %.2f", s.TotalCredits)
	}
	b.WriteByte('\n')
	if s.TotalInputTokens > 0 || s.TotalOutputTokens > 0 {
		fmt.Fprintf(&b, "  tokens:   %s in / %s out\n", formatCount(s.TotalInputTokens), formatCount(s.TotalOutputTokens))
	}
	b.WriteByte('\n')
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
	barW := min(max(interior-49, 3), 20)
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

func (m *model) renderSessionSubAgentCalls(s *SessionDetail) string {
	if len(s.SubAgentCalls) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", sectionStyle.Render("▸ Sub-Agent Calls"))
	for i, sa := range s.SubAgentCalls {
		ts := sa.Ts.Local().Format("15:04:05")
		dur := formatDur(time.Duration(sa.Duration))
		fmt.Fprintf(&b, "  %d. %s  %s  [%s]\n", i+1, ts, sa.AgentName, dur)
		if sa.Explanation != "" {
			fmt.Fprintf(&b, "     %s\n", mutedStyle.Render(sa.Explanation))
		}
		prompt := sa.Prompt
		if !m.timelineExpanded {
			prompt = truncate(singleLine(prompt), 120)
		}
		fmt.Fprintf(&b, "     Prompt: %s\n", prompt)
		if sa.Response != "" {
			resp := sa.Response
			if !m.timelineExpanded {
				resp = truncate(singleLine(resp), 120)
			}
			fmt.Fprintf(&b, "     Response: %s\n", mutedStyle.Render(resp))
		}
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

	groups := prepareSessionChanges(s.Changes)

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

	for _, g := range groups {
		shortPath := shortenPath(m.homeDir, g.Path, s.Cwd)
		editCount := len(g.Edits)
		editCountStr := fmt.Sprintf("%d edits", editCount)
		if editCount == 1 {
			editCountStr = "1 edit"
		}
		fileLastTs := g.LastTs.Local().Format("15:04:05")

		// Build file line: path  +N/-M  K edits  last HH:MM:SS
		var fileCounts string
		if g.OversizedCount == len(g.Edits) {
			// All oversized — no +/- badge.
			fileCounts = mutedStyle.Render("—")
		} else {
			fileCounts = addStyle.Render(fmt.Sprintf("+%d", g.TotalAdds)) + "/" + delStyle.Render(fmt.Sprintf("-%d", g.TotalDels))
		}
		metaStr := editCountStr + "  last " + fileLastTs
		if g.OversizedCount > 0 && g.OversizedCount < len(g.Edits) {
			metaStr += "  " + mutedStyle.Render(fmt.Sprintf("(%d oversized)", g.OversizedCount))
		}

		lineWidth := 2 + len(shortPath) + 2 + len(editCountStr) + 2 + len("last 00:00:00")
		if lineWidth > m.interiorWidth() {
			fmt.Fprintf(&b, "  %s  %s\n    %s\n", shortPath, fileCounts, metaStr)
		} else {
			fmt.Fprintf(&b, "  %s  %s  %s\n", shortPath, fileCounts, metaStr)
		}

		for _, fc := range g.Edits {
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
	summaryW := max(m.interiorWidth()-tlFixed, 10)
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
		if m.timelineExpanded && e.ToolInput != "" {
			for line := range strings.SplitSeq(e.ToolInput, "\n") {
				fmt.Fprintf(&b, "    %s\n", mutedStyle.Render(line))
			}
		}
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
