package monitor

import (
	"cmp"
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

var skillPathRe = regexp.MustCompile(`([a-zA-Z0-9_-]+)/SKILL\.md`)

const (
	maxRecentCalls       = 100
	maxErrors            = 50
	activeSessionTimeout = 5 * time.Minute
	maxSummaryLength     = 120
	maxErrorDetailLength      = 256
	maxAssistantResponseLength = 2048
)

// SessionMetric is an overview-level session summary retained for backwards compatibility.
type SessionMetric struct {
	ID           string
	AgentKey     string // composite key sid + "|" + agent; unique per (session, agent)
	Agent        string
	Title        string // first userPromptSubmit prompt, cleaned; may be empty
	Cwd          string
	StartTime    time.Time
	EndTime      time.Time
	LastActivity time.Time
	Duration     JSONDuration
	Active       bool // no stop event AND last event within 5min of now
	ToolCalls    int
	Prompts      int
}

// ToolMetric is an overview-level tool usage summary retained for backwards compatibility.
type ToolMetric struct {
	Name       string
	CallCount  int
	ErrorCount int // preToolUse without matching postToolUse, or non-zero exit_status
	ErrorRate  float64
}

// AgentMetric is an overview-level agent activity summary retained for backwards compatibility.
type AgentMetric struct {
	Name         string
	SessionCount int
	ToolCalls    int
	Prompts      int
}

// HourlyMetric is an hourly event count for activity chart display.
type HourlyMetric struct {
	Hour       time.Time // truncated to hour
	EventCount int
}

// Metrics is the aggregated overview metrics retained for backwards compatibility.
type Metrics struct {
	Sessions       []SessionMetric
	Tools          []ToolMetric
	Agents         []AgentMetric
	HourlyActivity []HourlyMetric
}

// EventEntry is one log event for timeline display.
type EventEntry struct {
	Ts           time.Time
	Event        string       // agentSpawn | userPromptSubmit | preToolUse | postToolUse | stop
	Tool         string
	IsError      bool         // preToolUse without matching postToolUse
	ErrorDetail  string       // exit code + stderr excerpt (max 256 chars), empty if no error
	InputSummary string       // short human-readable summary of tool_input (preToolUse only)
	Duration     JSONDuration // postToolUse.Ts - preToolUse.Ts (preToolUse only, 0 for errors)
}

// ToolCall is one completed tool invocation (preToolUse matched with postToolUse)
// or an unmatched preToolUse marked as error.
type ToolCall struct {
	Ts           time.Time // preToolUse timestamp
	Session      string
	Agent        string
	Tool         string
	Duration     JSONDuration // postToolUse.Ts - preToolUse.Ts (0 for errors)
	IsError      bool         // no matching postToolUse
	InputSummary string       // short human-readable summary of tool_input
}

// SessionToolSummary is a per-tool breakdown within a single session.
type SessionToolSummary struct {
	Tool        string
	CallCount   int
	ErrorCount  int
	SuccessRate float64      // (CallCount - ErrorCount) / CallCount
	AvgDuration JSONDuration
}

// SessionDetail is the per-session drill-down payload.
type SessionDetail struct {
	SessionMetric
	PromptHistory     []string             // raw prompts, newest first
	Timeline          []EventEntry         // full ordered event list for this session
	ToolSummary       []SessionToolSummary // per-tool breakdown, sorted by CallCount desc
	AssistantResponse string               // LLM final response from stop event (max 2KB)
}

// AgentDetail is the per-agent drill-down payload.
type AgentDetail struct {
	AgentMetric
	Sessions     []SessionMetric      // sessions owned by this agent, newest first
	ToolSummary  []SessionToolSummary // per-tool breakdown, sorted by CallCount desc
	ToolErrorCnt int                  // total error tool calls across all its sessions
}

// ToolDetail is the per-tool drill-down payload.
type ToolDetail struct {
	ToolMetric
	AvgDuration JSONDuration // average pre→post across matched calls
	RecentCalls []ToolCall   // newest first, matched calls with duration
	Errors      []ToolCall   // unmatched preToolUse samples
}

// SkillUsage counts how many times a skill's SKILL.md was read.
type SkillUsage struct {
	Name      string
	ReadCount int
}

// DetailedMetrics is the full aggregation result.
type DetailedMetrics struct {
	Overview Metrics
	Sessions []SessionDetail
	Agents   []AgentDetail
	Tools    []ToolDetail
	Skills   []SkillUsage
}

// Aggregate returns the overview metrics (kept for backward compat).
func Aggregate(records []Record, now time.Time) Metrics {
	return AggregateDetail(records, now).Overview
}

// pending is a queued preToolUse awaiting its postToolUse match.
type pending struct {
	tool    string // original tool name (bucket key may be tool + input hash)
	ts      time.Time
	index   int // index in timeline (so we can mark error later)
	summary string
}

type sessionState struct {
	id        string // sid (session identifier)
	agent     string
	cwd       string
	start     time.Time
	end       time.Time
	stopped   bool
	toolCalls int
	prompts   []string
	timeline  []EventEntry
	pending   map[string][]pending // bucket key -> queue of unmatched preToolUse
	sumTitle  string               // latest summary-tool taskDescription (if any)
	assistantResponse string       // from stop event
}

// aggState holds the mutable accumulators shared by the three
// AggregateDetail phases: processRecord, finalizeSessionStats, assembleDetails.
type aggState struct {
	now            time.Time
	sessions       map[string]*sessionState
	tools          map[string]*ToolDetail
	agents         map[string]*AgentDetail
	hours          map[time.Time]int
	skills         map[string]int
	sessionDetails []SessionDetail
}

func newAggState(n int, now time.Time) *aggState {
	return &aggState{
		now:      now,
		sessions: make(map[string]*sessionState, n),
		tools:    map[string]*ToolDetail{},
		agents:   map[string]*AgentDetail{},
		hours:    map[time.Time]int{},
		skills:   map[string]int{},
	}
}

// AggregateDetail walks the record stream once and builds everything the TUI needs.
func AggregateDetail(records []Record, now time.Time) DetailedMetrics {
	if len(records) == 0 {
		return DetailedMetrics{}
	}

	// Sort records chronologically once so timelines and pre/post matching are ordered.
	sorted := make([]Record, len(records))
	copy(sorted, records)
	slices.SortStableFunc(sorted, func(a, b Record) int {
		if c := a.Ts.Compare(b.Ts); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Session, b.Session); c != 0 {
			return c
		}
		return cmp.Compare(a.Agent, b.Agent)
	})

	st := newAggState(len(sorted), now)
	for _, r := range sorted {
		processRecord(st, r)
	}
	finalizeSessionStats(st)
	return assembleDetails(st)
}

// processRecord folds one record into the aggregation state.
func processRecord(st *aggState, r Record) {
	st.hours[r.Ts.Truncate(time.Hour)]++

	// Re-key shell tool calls into derived per-command buckets so metrics can
	// distinguish "git push failing" from "ls succeeding". All downstream
	// aggregation keys off r.Tool, so a single rewrite propagates everywhere.
	if r.Tool == "shell" && (r.Event == apmconfig.EventPreToolUse || r.Event == apmconfig.EventPostToolUse) {
		r.Tool = classifyShell(r.ToolInput, r.Cwd)
	}

	s := touchSessionState(st, r)
	s.timeline = append(s.timeline, EventEntry{Ts: r.Ts, Event: r.Event, Tool: r.Tool})
	idx := len(s.timeline) - 1

	switch r.Event {
	case apmconfig.EventStop:
		s.stopped = true
		s.assistantResponse = parseAssistantResponse(r.AssistantResponse)
	case apmconfig.EventUserPromptSubmit:
		s.prompts = append(s.prompts, r.Prompt)
	case apmconfig.EventPreToolUse:
		recordPreToolUse(st, s, r, idx)
	case apmconfig.EventPostToolUse:
		resolvePostToolUse(st, s, r)
	}
}

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
	if r.Tool == "read" && len(r.ToolInput) > 0 {
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

// finalizeSessionStats marks unmatched preToolUse as errors, builds per-session
// details, and folds them into agent stats.
func finalizeSessionStats(st *aggState) {
	markUnmatchedToolCalls(st)
	buildSessionDetails(st)
	foldSessionIntoAgents(st)
	for _, td := range st.tools {
		finalizeToolDetails(td)
	}
}

// newSessionMetric builds the SessionMetric summary for a session.
func newSessionMetric(s *sessionState, now time.Time) SessionMetric {
	active := !s.stopped && now.Sub(s.end) <= activeSessionTimeout
	var title string
	switch {
	case s.sumTitle != "":
		title = cleanSummary(s.sumTitle)
	case len(s.prompts) > 0:
		title = cleanSummary(s.prompts[0])
	}
	return SessionMetric{
		ID: s.id, AgentKey: compositeKey(s.id, s.agent), Agent: s.agent, Title: title, Cwd: s.cwd,
		StartTime: s.start, EndTime: s.end, LastActivity: s.end, Duration: JSONDuration(s.end.Sub(s.start)),
		Active: active, ToolCalls: s.toolCalls, Prompts: len(s.prompts),
	}
}

// markUnmatchedToolCalls walks pending buckets and marks residual pre-events as errors.
func markUnmatchedToolCalls(st *aggState) {
	for _, s := range st.sessions {
		for _, q := range s.pending {
			for _, p := range q {
				s.timeline[p.index].IsError = true
				td := toolEntry(st.tools, p.tool)
				td.ErrorCount++
				td.Errors = append(td.Errors, ToolCall{
					Ts: p.ts, Session: s.id, Agent: s.agent, Tool: p.tool, IsError: true,
					InputSummary: p.summary,
				})
			}
		}
	}
}

// buildSessionDetails builds per-session SessionDetail entries and appends to st.sessionDetails.
func buildSessionDetails(st *aggState) {
	for _, s := range st.sessions {
		base := newSessionMetric(s, st.now)
		promptsRev := slices.Clone(s.prompts)
		if promptsRev == nil {
			promptsRev = []string{}
		}
		slices.Reverse(promptsRev)
		st.sessionDetails = append(st.sessionDetails, SessionDetail{
			SessionMetric:     base,
			PromptHistory:     promptsRev,
			Timeline:          s.timeline,
			ToolSummary:       sessionToolSummary(s.timeline),
			AssistantResponse: s.assistantResponse,
		})
	}
}

// sessionToolSummary computes per-tool stats from a session timeline.
// Returns nil if there are no tool calls.
func sessionToolSummary(timeline []EventEntry) []SessionToolSummary {
	type agg struct {
		callCount  int
		errorCount int
		durSum     time.Duration
		durCount   int
	}
	m := map[string]*agg{}
	for _, ev := range timeline {
		if ev.Event != apmconfig.EventPreToolUse {
			continue
		}
		a := m[ev.Tool]
		if a == nil {
			a = &agg{}
			m[ev.Tool] = a
		}
		a.callCount++
		if ev.IsError {
			a.errorCount++
		} else if ev.Duration > 0 {
			a.durSum += time.Duration(ev.Duration)
			a.durCount++
		}
	}
	if len(m) == 0 {
		return nil
	}
	out := make([]SessionToolSummary, 0, len(m))
	for tool, a := range m {
		var avg JSONDuration
		if a.durCount > 0 {
			avg = JSONDuration(a.durSum / time.Duration(a.durCount))
		}
		var rate float64
		if a.callCount > 0 {
			rate = float64(a.callCount-a.errorCount) / float64(a.callCount)
		}
		out = append(out, SessionToolSummary{
			Tool: tool, CallCount: a.callCount, ErrorCount: a.errorCount,
			SuccessRate: rate, AvgDuration: avg,
		})
	}
	slices.SortFunc(out, func(a, b SessionToolSummary) int {
		if c := cmp.Compare(b.CallCount, a.CallCount); c != 0 {
			return c
		}
		return cmp.Compare(a.Tool, b.Tool)
	})
	return out
}

// sessionTimeseries buckets preToolUse events from a session timeline.
// foldSessionIntoAgents folds every session into agent aggregation and sorts sessions newest-first.
func foldSessionIntoAgents(st *aggState) {
	// Temporary per-agent tool aggregation.
	type toolAgg struct {
		callCount  int
		errorCount int
		durSum     time.Duration
		durCount   int
	}
	agentTools := map[string]map[string]*toolAgg{} // agent -> tool -> agg

	for _, s := range st.sessions {
		a, ok := st.agents[s.agent]
		if !ok {
			a = &AgentDetail{AgentMetric: AgentMetric{Name: s.agent}}
			st.agents[s.agent] = a
		}
		a.SessionCount++
		a.ToolCalls += s.toolCalls
		a.Prompts += len(s.prompts)
		a.Sessions = append(a.Sessions, newSessionMetric(s, st.now))

		if agentTools[s.agent] == nil {
			agentTools[s.agent] = map[string]*toolAgg{}
		}
		tm := agentTools[s.agent]
		for _, ev := range s.timeline {
			if ev.Event == apmconfig.EventPreToolUse {
				ta := tm[ev.Tool]
				if ta == nil {
					ta = &toolAgg{}
					tm[ev.Tool] = ta
				}
				ta.callCount++
				if ev.IsError {
					ta.errorCount++
					a.ToolErrorCnt++
				} else if ev.Duration > 0 {
					ta.durSum += time.Duration(ev.Duration)
					ta.durCount++
				}
			}
		}
	}
	for name, a := range st.agents {
		slices.SortFunc(a.Sessions, func(x, y SessionMetric) int {
			if c := y.StartTime.Compare(x.StartTime); c != 0 {
				return c
			}
			return cmp.Compare(x.AgentKey, y.AgentKey)
		})
		tm := agentTools[name]
		out := make([]SessionToolSummary, 0, len(tm))
		for tool, ta := range tm {
			var rate float64
			if ta.callCount > 0 {
				rate = float64(ta.callCount-ta.errorCount) / float64(ta.callCount)
			}
			var avg JSONDuration
			if ta.durCount > 0 {
				avg = JSONDuration(ta.durSum / time.Duration(ta.durCount))
			}
			out = append(out, SessionToolSummary{
				Tool: tool, CallCount: ta.callCount, ErrorCount: ta.errorCount,
				SuccessRate: rate, AvgDuration: avg,
			})
		}
		slices.SortFunc(out, func(a, b SessionToolSummary) int {
			if c := cmp.Compare(b.CallCount, a.CallCount); c != 0 {
				return c
			}
			return cmp.Compare(a.Tool, b.Tool)
		})
		a.ToolSummary = out
	}
}

// finalizeToolDetails computes ErrorRate/AvgDuration and sorts
// RecentCalls/Errors newest-first with a per-slice cap.
func finalizeToolDetails(td *ToolDetail) {
	if td.CallCount > 0 {
		td.ErrorRate = float64(td.ErrorCount) / float64(td.CallCount)
	}
	if len(td.RecentCalls) > 0 {
		var total time.Duration
		for _, c := range td.RecentCalls {
			total += time.Duration(c.Duration)
		}
		td.AvgDuration = JSONDuration(total / time.Duration(len(td.RecentCalls)))
		slices.SortFunc(td.RecentCalls, func(a, b ToolCall) int {
			if c := b.Ts.Compare(a.Ts); c != 0 {
				return c
			}
			if c := cmp.Compare(a.Session, b.Session); c != 0 {
				return c
			}
			if c := cmp.Compare(a.Agent, b.Agent); c != 0 {
				return c
			}
			if c := cmp.Compare(a.Tool, b.Tool); c != 0 {
				return c
			}
			return cmp.Compare(a.InputSummary, b.InputSummary)
		})
		if len(td.RecentCalls) > maxRecentCalls {
			td.RecentCalls = td.RecentCalls[:maxRecentCalls]
		}
	}
	slices.SortFunc(td.Errors, func(a, b ToolCall) int {
		if c := b.Ts.Compare(a.Ts); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Session, b.Session); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Agent, b.Agent); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Tool, b.Tool); c != 0 {
			return c
		}
		return cmp.Compare(a.InputSummary, b.InputSummary)
	})
	if len(td.Errors) > maxErrors {
		td.Errors = td.Errors[:maxErrors]
	}
}

// assembleDetails produces the final DetailedMetrics with sorted overview and detail slices.
func assembleDetails(st *aggState) DetailedMetrics {
	overview := Metrics{}
	slices.SortStableFunc(st.sessionDetails, func(a, b SessionDetail) int {
		if !a.LastActivity.Equal(b.LastActivity) {
			return b.LastActivity.Compare(a.LastActivity)
		}
		if !a.StartTime.Equal(b.StartTime) {
			return b.StartTime.Compare(a.StartTime)
		}
		return cmp.Compare(a.AgentKey, b.AgentKey)
	})
	for _, sd := range st.sessionDetails {
		overview.Sessions = append(overview.Sessions, sd.SessionMetric)
	}

	toolNames := slices.Sorted(maps.Keys(st.tools))
	toolDetails := make([]ToolDetail, 0, len(toolNames))
	for _, n := range toolNames {
		td := st.tools[n]
		td.Name = n
		overview.Tools = append(overview.Tools, td.ToolMetric)
		toolDetails = append(toolDetails, *td)
	}

	agentNames := slices.Sorted(maps.Keys(st.agents))
	agentDetails := make([]AgentDetail, 0, len(agentNames))
	for _, n := range agentNames {
		a := st.agents[n]
		overview.Agents = append(overview.Agents, a.AgentMetric)
		agentDetails = append(agentDetails, *a)
	}

	for h, count := range st.hours {
		overview.HourlyActivity = append(overview.HourlyActivity, HourlyMetric{Hour: h, EventCount: count})
	}
	slices.SortFunc(overview.HourlyActivity, func(a, b HourlyMetric) int { return a.Hour.Compare(b.Hour) })

	// Pre-sort overview and detail slices by usage (descending) so render functions don't need to sort per frame.
	slices.SortFunc(overview.Tools, func(a, b ToolMetric) int {
		if c := cmp.Compare(b.CallCount, a.CallCount); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	slices.SortFunc(overview.Agents, func(a, b AgentMetric) int {
		if c := cmp.Compare(b.ToolCalls, a.ToolCalls); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	slices.SortFunc(agentDetails, func(a, b AgentDetail) int {
		if c := cmp.Compare(b.ToolCalls, a.ToolCalls); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	slices.SortFunc(toolDetails, func(a, b ToolDetail) int {
		if c := cmp.Compare(b.CallCount, a.CallCount); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})

	return DetailedMetrics{
		Overview: overview,
		Sessions: st.sessionDetails,
		Agents:   agentDetails,
		Tools:    toolDetails,
		Skills:   skillsList(st.skills),
	}
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

// toolInput is a lenient typed view of tool_input payloads. Fields absent in
// the payload remain zero-valued; unknown fields are ignored (tool producers
// may send extra fields).
type toolInput struct {
	Operations []operation `json:"operations,omitempty"`
	Path       string      `json:"path,omitempty"`
	Paths      []string    `json:"paths,omitempty"`
	Command    string      `json:"command,omitempty"`
	Purpose    string      `json:"__tool_use_purpose,omitempty"`
	FilePath   string      `json:"file_path,omitempty"`
	Pattern    string      `json:"pattern,omitempty"`
	Prompt     string      `json:"prompt,omitempty"`
	Content    string      `json:"content,omitempty"`
	SymbolName string      `json:"symbol_name,omitempty"`
	NewName    string      `json:"new_name,omitempty"`
	Query      string      `json:"query,omitempty"`
}

// operation describes a single entry in tool_input.operations (used by read).
type operation struct {
	Mode       string   `json:"mode,omitempty"`
	Path       string   `json:"path,omitempty"`
	FilePath   string   `json:"file_path,omitempty"`
	ImagePaths []string `json:"image_paths,omitempty"`
	Offset     int      `json:"offset,omitempty"`
	Limit      int      `json:"limit,omitempty"`
}

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

func itoa(n int) string { return strconv.Itoa(n) }

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
		out = out[:maxSummaryLength-1] + "…"
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
		if len(stderr) > maxErrorDetailLength {
			stderr = stderr[:maxErrorDetailLength]
		}
		return "exit " + itoa(n) + ": " + stderr
	}
	if item.Text != "" {
		if len(item.Text) > maxErrorDetailLength {
			return item.Text[:maxErrorDetailLength]
		}
		return item.Text
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
		s = s[:maxAssistantResponseLength]
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

// AggregateToolsFromTimeline recomputes per-tool details and overview metrics
// from a subset of SessionDetails. Used by API filters (by session/agent) to
// produce Tools data consistent with the filtered session set.
//
// Each SessionDetail's Timeline is walked: preToolUse events increment
// CallCount, flagged IsError events increment ErrorCount (and append to
// Errors), and matched pairs (with non-zero Duration on the preToolUse entry)
// append to RecentCalls. Session and Agent attribution on each ToolCall is
// taken from the enclosing SessionDetail, since EventEntry does not preserve
// those fields.
//
// Skills are not re-aggregated here (raw Record access is required) and must
// be populated separately.
func AggregateToolsFromTimeline(sessions []SessionDetail) ([]ToolDetail, []ToolMetric) {
	if len(sessions) == 0 {
		return nil, nil
	}
	tools := map[string]*ToolDetail{}
	for _, sd := range sessions {
		for _, ev := range sd.Timeline {
			if ev.Event != apmconfig.EventPreToolUse {
				continue
			}
			td := toolEntry(tools, ev.Tool)
			td.CallCount++
			call := ToolCall{
				Ts: ev.Ts, Session: sd.ID, Agent: sd.Agent, Tool: ev.Tool,
				Duration: ev.Duration, IsError: ev.IsError, InputSummary: ev.InputSummary,
			}
			if ev.IsError {
				td.ErrorCount++
				td.Errors = append(td.Errors, call)
			} else {
				td.RecentCalls = append(td.RecentCalls, call)
			}
		}
	}
	for _, td := range tools {
		finalizeToolDetails(td)
	}
	names := slices.Sorted(maps.Keys(tools))
	details := make([]ToolDetail, 0, len(names))
	metrics := make([]ToolMetric, 0, len(names))
	for _, n := range names {
		td := tools[n]
		td.Name = n
		details = append(details, *td)
		metrics = append(metrics, td.ToolMetric)
	}
	slices.SortFunc(details, func(a, b ToolDetail) int {
		if c := cmp.Compare(b.CallCount, a.CallCount); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	slices.SortFunc(metrics, func(a, b ToolMetric) int {
		if c := cmp.Compare(b.CallCount, a.CallCount); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return details, metrics
}

// TimeseriesPoint is one time-bucket of aggregated tool calls.
type TimeseriesPoint struct {
	Bucket      time.Time    `json:"bucket"`
	Count       int          `json:"count"`
	AvgDuration JSONDuration `json:"avgDuration"`
	ErrorCount  int          `json:"errorCount"`
}

// PatternCount is one InputSummary pattern with its call count.
type PatternCount struct {
	Summary string    `json:"summary"`
	Count   int       `json:"count"`
	LastTs  time.Time `json:"lastTs"`
}

// AggregateToolTimeseries groups calls into time buckets (1min if window ≤ 2h, else 5min).
// Returns nil if fewer than 2 distinct buckets.
func AggregateToolTimeseries(calls []ToolCall, now time.Time) []TimeseriesPoint {
	if len(calls) == 0 {
		return nil
	}
	earliest, latest := calls[0].Ts, calls[0].Ts
	for _, c := range calls[1:] {
		if c.Ts.Before(earliest) {
			earliest = c.Ts
		}
		if c.Ts.After(latest) {
			latest = c.Ts
		}
	}
	bucket := time.Minute
	if latest.Sub(earliest) > 2*time.Hour {
		bucket = 5 * time.Minute
	}

	type agg struct {
		count      int
		errorCount int
		durSum     time.Duration
		durCount   int
	}
	m := map[time.Time]*agg{}
	for _, c := range calls {
		key := c.Ts.Truncate(bucket)
		a := m[key]
		if a == nil {
			a = &agg{}
			m[key] = a
		}
		a.count++
		if c.IsError {
			a.errorCount++
		} else if c.Duration > 0 {
			a.durSum += time.Duration(c.Duration)
			a.durCount++
		}
	}
	if len(m) < 2 {
		return nil
	}
	keys := make([]time.Time, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b time.Time) int { return a.Compare(b) })
	pts := make([]TimeseriesPoint, len(keys))
	for i, k := range keys {
		a := m[k]
		var avg JSONDuration
		if a.durCount > 0 {
			avg = JSONDuration(a.durSum / time.Duration(a.durCount))
		}
		pts[i] = TimeseriesPoint{Bucket: k, Count: a.count, AvgDuration: avg, ErrorCount: a.errorCount}
	}
	return pts
}

// AggregateToolInputPatterns groups calls by InputSummary and returns the top-N
// sorted by Count desc, LastTs desc, Summary asc.
func AggregateToolInputPatterns(calls []ToolCall, topN int) []PatternCount {
	type agg struct {
		count  int
		lastTs time.Time
	}
	m := map[string]*agg{}
	for _, c := range calls {
		key := c.InputSummary
		if key == "" {
			key = "(empty)"
		}
		a := m[key]
		if a == nil {
			a = &agg{}
			m[key] = a
		}
		a.count++
		if c.Ts.After(a.lastTs) {
			a.lastTs = c.Ts
		}
	}
	pats := make([]PatternCount, 0, len(m))
	for k, a := range m {
		pats = append(pats, PatternCount{Summary: k, Count: a.count, LastTs: a.lastTs})
	}
	slices.SortFunc(pats, func(a, b PatternCount) int {
		if a.Count != b.Count {
			return cmp.Compare(b.Count, a.Count)
		}
		if !a.LastTs.Equal(b.LastTs) {
			if a.LastTs.After(b.LastTs) {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Summary, b.Summary)
	})
	if topN > 0 && len(pats) > topN {
		pats = pats[:topN]
	}
	return pats
}
