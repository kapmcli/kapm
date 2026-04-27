package monitor

import (
	"cmp"
	"encoding/json"
	"strings"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

// touchSessionState looks up or creates the session keyed by (session, agent),
// then updates cwd and end timestamp from the record.
func touchSessionState(st *aggState, r Record) *sessionState {
	agent := r.Agent
	if agent == "" {
		agent = "(unknown)" // sentinel: uses chars outside [A-Za-z0-9._-] so it cannot collide with a real agent
	}
	key := compositeKey(r.Session, agent)
	s, ok := st.sessions[key]
	if !ok {
		s = &sessionState{id: r.Session, agent: agent, cwd: r.Cwd, start: r.Ts, end: r.Ts, pending: map[string][]pending{}}
		st.sessions[key] = s
	}
	if r.Cwd != "" {
		s.cwd = r.Cwd
	}
	if r.Ts.After(s.end) {
		s.end = r.Ts
	}
	return s
}

// recordPreToolUse handles the PreToolUse branch: increments toolCalls, sets
// the timeline InputSummary, extracts summary title, enqueues a pending entry,
// increments the tool CallCount, and tracks skill-path reads.
func recordPreToolUse(st *aggState, s *sessionState, r Record, idx int) {
	s.toolCalls++
	summary := inputSummary(r.ToolInput, r.Tool, s.cwd)
	s.timeline[idx].InputSummary = summary
	if r.Tool == "summary" {
		if td := extractSummaryTitle(r.ToolInput); td != "" {
			s.sumTitle = td
		}
	}
	key := pendingKey(r.Tool, r.ToolInput)
	s.pending[key] = append(s.pending[key], pending{tool: r.Tool, ts: r.Ts, index: idx, summary: summary})
	toolEntry(st.tools, r.Tool).CallCount++
	if r.Tool == apmconfig.ToolRead && len(r.ToolInput) > 0 {
		if match := skillPathRe.FindSubmatch(r.ToolInput); match != nil {
			st.skills[string(match[1])]++
		}
	}
}

// resolvePostToolUse handles the PostToolUse branch: pops the matching pending
// entry (with oldest-fallback), computes duration, and routes the call into
// either the Errors or RecentCalls bucket based on exit status.
func resolvePostToolUse(st *aggState, s *sessionState, r Record) {
	key := pendingKey(r.Tool, r.ToolInput)
	q, bucketKey := s.pending[key], key
	if len(q) == 0 {
		// Fallback: post may have been re-serialized differently or
		// have an empty input (as in some tests). Take the oldest
		// pending entry for the same tool regardless of bucket.
		q, bucketKey = oldestPendingForTool(s.pending, r.Tool)
	}
	if len(q) == 0 {
		return
	}
	p := q[0]
	s.pending[bucketKey] = q[1:]
	dur := JSONDuration(r.Ts.Sub(p.ts))
	s.timeline[p.index].Duration = dur
	td := toolEntry(st.tools, r.Tool)
	call := ToolCall{
		Ts: p.ts, Session: r.Session, Agent: s.agent, Tool: r.Tool,
		Duration: dur, InputSummary: p.summary,
	}
	if parseToolResponseError(r.ToolResponse) {
		s.timeline[p.index].IsError = true
		s.timeline[p.index].ErrorDetail = parseErrorDetail(r.ToolResponse)
		call.IsError = true
		td.ErrorCount++
		td.Errors = append(td.Errors, call)
	} else {
		td.RecentCalls = append(td.RecentCalls, call)
	}
}

// compositeKey returns the (sid, agent) aggregation key used to uniquely
// identify a session-per-agent. The sid is a UUID ([a-zA-Z0-9-]+), so "|" is
// a safe separator that will not appear in either component.
func compositeKey(sid, agent string) string {
	return sid + "|" + agent
}

// pendingKey returns the FIFO bucket key for matching preToolUse with
// postToolUse. The hook copies tool_input verbatim into both events, so the
// raw bytes are already canonical and can be used directly.
func pendingKey(tool string, input json.RawMessage) string {
	if len(input) == 0 {
		return tool
	}
	return tool + "|" + string(input)
}

// oldestPendingForTool finds the pending bucket containing the earliest
// preToolUse entry across all input-scoped buckets for the given tool.
// Used as a fallback when a postToolUse event cannot be matched by canonical
// input (e.g. differing serialization, or missing input in tests).
func oldestPendingForTool(buckets map[string][]pending, tool string) ([]pending, string) {
	prefix := tool + "|"
	var bestKey string
	var best []pending
	for k, q := range buckets {
		if k != tool && !strings.HasPrefix(k, prefix) {
			continue
		}
		if len(q) == 0 {
			continue
		}
		qFirst, _ := first(q)
		bestFirst, bestOk := first(best)
		if !bestOk || qFirst.ts.Before(bestFirst.ts) || (qFirst.ts.Equal(bestFirst.ts) && cmp.Compare(k, bestKey) < 0) {
			best, bestKey = q, k
		}
	}
	return best, bestKey
}
