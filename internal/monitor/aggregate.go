package monitor

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

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
// Returns ctx.Err() if ctx is cancelled during record processing or session
// finalization. Returns a zero DetailedMetrics when there are no records.
func AggregateDetail(ctx context.Context, records []Record, now time.Time) (DetailedMetrics, error) {
	if len(records) == 0 {
		return DetailedMetrics{}, nil
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
	for i, r := range sorted {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return DetailedMetrics{}, err
			}
		}
		processRecord(st, r)
	}
	if err := ctx.Err(); err != nil {
		return DetailedMetrics{}, err
	}
	finalizeSessionStats(st)
	return assembleDetails(st), nil
}

// processRecord folds one record into the aggregation state.
func processRecord(st *aggState, r Record) {
	st.hours[r.Ts.Truncate(time.Hour)]++

	// Re-key shell tool calls into derived per-command buckets so metrics can
	// distinguish "git push failing" from "ls succeeding". All downstream
	// aggregation keys off r.Tool, so a single rewrite propagates everywhere.
	if r.Tool == apmconfig.ToolShell && (r.Event == apmconfig.EventPreToolUse || r.Event == apmconfig.EventPostToolUse) {
		r.Tool = classifyShell(r.ToolInput, r.Cwd)
	}

	s := touchSessionState(st, r)
	s.timeline = append(s.timeline, EventEntry{Ts: r.Ts, Event: r.Event, Tool: r.Tool})
	idx := len(s.timeline) - 1

	if r.Event == apmconfig.EventPreToolUse && r.Tool == apmconfig.ToolWrite {
		if fc, ok := parseWriteInput(r.ToolInput, r.Ts, s.cwd); ok {
			s.changes = append(s.changes, fc)
		}
	}

	switch r.Event {
	case apmconfig.EventStop:
		s.isStopped = true
		s.assistantResponse = parseAssistantResponse(r.AssistantResponse)
	case apmconfig.EventUserPromptSubmit:
		s.prompts = append(s.prompts, r.Prompt)
	case apmconfig.EventPreToolUse:
		recordPreToolUse(st, s, r, idx)
	case apmconfig.EventPostToolUse:
		resolvePostToolUse(st, s, r)
	}
}

// finalizeSessionStats marks unmatched preToolUse as errors, builds per-session
// details, and folds them into agent stats.
func finalizeSessionStats(st *aggState) {
	markUnmatchedToolCalls(st)
	for _, s := range st.sessions {
		s.filesChangedCached = countUniqueFiles(s.changes)
	}
	buildSessionDetails(st)
	foldSessionIntoAgents(st)
	for _, td := range st.tools {
		finalizeToolDetails(td)
	}
}

// newSessionMetric builds the SessionMetric summary for a session.
func newSessionMetric(s *sessionState, now time.Time) SessionMetric {
	active := !s.isStopped && now.Sub(s.end) <= activeSessionTimeout
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
		FilesChanged: s.filesChangedCached,
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
		var changes []FileChange
		if len(s.changes) > 0 {
			changes = slices.Clone(s.changes)
			slices.SortStableFunc(changes, func(a, b FileChange) int { return a.Ts.Compare(b.Ts) })
		}
		st.sessionDetails = append(st.sessionDetails, SessionDetail{
			SessionMetric:     base,
			PromptHistory:     promptsRev,
			Timeline:          s.timeline,
			ToolSummary:       sessionToolSummary(s.timeline),
			AssistantResponse: s.assistantResponse,
			Changes:           changes,
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

// toolAggToSummary converts a *toolAgg into a SessionToolSummary.
func toolAggToSummary(tool string, ta *toolAgg) SessionToolSummary {
	var rate float64
	if ta.callCount > 0 {
		rate = float64(ta.callCount-ta.errorCount) / float64(ta.callCount)
	}
	var avg JSONDuration
	if ta.durCount > 0 {
		avg = JSONDuration(ta.durSum / time.Duration(ta.durCount))
	}
	return SessionToolSummary{
		Tool: tool, CallCount: ta.callCount, ErrorCount: ta.errorCount,
		SuccessRate: rate, AvgDuration: avg,
	}
}

// foldSessionIntoAgents folds every session into agent aggregation in a single
// pass: per-agent metrics, per-tool aggregation, and ToolErrorCnt are all built
// by walking st.sessions once.
func foldSessionIntoAgents(st *aggState) {
	agentTools := map[string]map[string]*toolAgg{}
	for _, s := range st.sessions {
		a, ok := st.agents[s.agent]
		if !ok {
			a = &AgentDetail{AgentMetric: AgentMetric{Name: s.agent}}
			st.agents[s.agent] = a
		}
		a.SessionCount++
		a.ToolCalls += s.toolCalls
		a.Prompts += len(s.prompts)
		a.FilesChanged += s.filesChangedCached
		a.Sessions = append(a.Sessions, newSessionMetric(s, st.now))

		tm := agentTools[s.agent]
		if tm == nil {
			tm = map[string]*toolAgg{}
			agentTools[s.agent] = tm
		}
		for _, ev := range s.timeline {
			if ev.Event != apmconfig.EventPreToolUse {
				continue
			}
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
			out = append(out, toolAggToSummary(tool, ta))
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
