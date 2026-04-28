package monitor

import (
	"cmp"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

// inputSummary extracts a short human-readable summary of a tool_input payload.
// Returns "" for nil/empty/unparseable input. The tool name selects a
// registered formatter; unknown tools fall through to genericSummary.
func inputSummary(raw json.RawMessage, tool, cwd string) string {
	if len(raw) == 0 {
		return ""
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var in toolInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return cleanSummary(string(raw))
	}
	if f, ok := toolFormatters[baseToolName(tool)]; ok {
		if s, ok := f(in, cwd); ok {
			return cleanSummary(s)
		}
	}
	return genericSummary(raw, in)
}

// genericSummary is the fallback for unknown tools or when a registered
// formatter returns ok=false. It prefers typed fields, then alphabetical
// first string key, then compact JSON.
func genericSummary(raw json.RawMessage, in toolInput) string {
	var extras map[string]any
	_ = json.Unmarshal(raw, &extras)
	if len(extras) == 0 {
		return cleanSummary(string(raw))
	}
	for _, v := range []string{in.Command, in.Path, in.FilePath, in.Pattern, in.Prompt, in.Content, in.SymbolName, in.NewName, in.Query} {
		if v != "" {
			return cleanSummary(v)
		}
	}
	if len(in.Operations) > 0 {
		op := in.Operations[0]
		if op.Path != "" {
			return cleanSummary(op.Path)
		}
		if op.FilePath != "" {
			return cleanSummary(op.FilePath)
		}
		if len(op.ImagePaths) > 0 && op.ImagePaths[0] != "" {
			return cleanSummary(op.ImagePaths[0])
		}
	}
	keys := make([]string, 0, len(extras))
	for k := range extras {
		if k != "__tool_use_purpose" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		if v, ok := extras[k].(string); ok && v != "" {
			return cleanSummary(v)
		}
	}
	if in.Purpose != "" {
		return cleanSummary(in.Purpose)
	}
	b, err := json.Marshal(extras)
	if err != nil {
		return ""
	}
	return cleanSummary(string(b))
}

// stripCdToCwd removes a leading "cd <cwd> &&" (or with ;) prefix when the
// target matches the session's working directory. Other cds are preserved
// because they carry semantic meaning (subshell navigation).
func stripCdToCwd(cmd, cwd string) string {
	if cwd == "" {
		return cmd
	}
	trimmed := strings.TrimLeft(cmd, " \t")
	if !strings.HasPrefix(trimmed, "cd ") {
		return cmd
	}
	rest := trimmed[3:]
	// Match target path (bare, single-quoted, or double-quoted).
	var target, tail string
	switch {
	case strings.HasPrefix(rest, `"`):
		if i := strings.Index(rest[1:], `"`); i >= 0 {
			target, tail = rest[1:1+i], rest[2+i:]
		}
	case strings.HasPrefix(rest, `'`):
		if i := strings.Index(rest[1:], `'`); i >= 0 {
			target, tail = rest[1:1+i], rest[2+i:]
		}
	default:
		if i := strings.IndexAny(rest, " \t;&"); i >= 0 {
			target, tail = rest[:i], rest[i:]
		} else {
			target = rest
		}
	}
	if target != cwd {
		return cmd
	}
	tail = strings.TrimLeft(tail, " \t")
	tail = strings.TrimPrefix(tail, "&&")
	tail = strings.TrimPrefix(tail, ";")
	return strings.TrimLeft(tail, " \t")
}

// cleanSummary collapses whitespace/control chars and truncates to 120 chars.
func cleanSummary(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' || (r < 0x20) {
			sb.WriteByte(' ')
			continue
		}
		sb.WriteRune(r)
	}
	out := strings.Join(strings.Fields(sb.String()), " ")
	if len(out) > maxSummaryLength {
		out = truncateUTF8(out, maxSummaryLength-len("…")) + "…"
	}
	return out
}

// extractSummaryTitle returns the taskDescription field from a summary tool's
// tool_input JSON, or empty string if absent.
func extractSummaryTitle(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m struct {
		TaskDescription string `json:"taskDescription"`
	}
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}
	return m.TaskDescription
}

// parseToolResponseError returns true when the tool_response JSON contains an
// exit_status with the literal prefix "exit status: " followed by a non-zero
// integer. All other cases (nil, malformed, missing fields, zero) return false.
func parseToolResponseError(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var resp struct {
		Items []struct {
			JSON *struct {
				ExitStatus string `json:"exit_status"`
			} `json:"Json"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || len(resp.Items) == 0 {
		return false
	}
	j := resp.Items[0].JSON
	if j == nil {
		return false
	}
	const prefix = "exit status: "
	if !strings.HasPrefix(j.ExitStatus, prefix) {
		return false
	}
	n, err := strconv.Atoi(j.ExitStatus[len(prefix):])
	return err == nil && n != 0
}

// parseErrorDetail extracts a human-readable error detail string from a tool_response.
// For shell responses: "exit <N>: <stderr excerpt>". For non-shell Text items: the text excerpt.
// Returns empty string when there is no error or the input is nil/empty.
func parseErrorDetail(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var resp struct {
		Items []struct {
			JSON *struct {
				ExitStatus string `json:"exit_status"`
				Stderr     string `json:"stderr"`
			} `json:"Json"`
			Text string `json:"Text"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || len(resp.Items) == 0 {
		return ""
	}
	item := resp.Items[0]
	if item.JSON != nil {
		const prefix = "exit status: "
		if !strings.HasPrefix(item.JSON.ExitStatus, prefix) {
			return ""
		}
		n, err := strconv.Atoi(item.JSON.ExitStatus[len(prefix):])
		if err != nil || n == 0 {
			return ""
		}
		stderr := item.JSON.Stderr
		stderr = truncateUTF8(stderr, maxErrorDetailLength)
		return "exit " + strconv.Itoa(n) + ": " + stderr
	}
	if item.Text != "" {
		return truncateUTF8(item.Text, maxErrorDetailLength)
	}
	return ""
}

// parseAssistantResponse unwraps a JSON-encoded string assistant response,
// falling back to the raw bytes if unmarshal fails. Returns empty for nil/empty input.
// Output is capped at maxAssistantResponseLength bytes.
func parseAssistantResponse(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		s = string(raw)
	}
	if len(s) > maxAssistantResponseLength {
		s = truncateUTF8(s, maxAssistantResponseLength)
	}
	return s
}

func toolEntry(tools map[string]*ToolDetail, name string) *ToolDetail {
	td, ok := tools[name]
	if !ok {
		td = &ToolDetail{ToolMetric: ToolMetric{Name: name}}
		tools[name] = td
	}
	return td
}

func skillsList(counts map[string]int) []SkillUsage {
	if len(counts) == 0 {
		return nil
	}
	out := make([]SkillUsage, 0, len(counts))
	for name, c := range counts {
		out = append(out, SkillUsage{Name: name, ReadCount: c})
	}
	slices.SortFunc(out, func(a, b SkillUsage) int {
		if a.ReadCount != b.ReadCount {
			return cmp.Compare(b.ReadCount, a.ReadCount)
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return out
}
