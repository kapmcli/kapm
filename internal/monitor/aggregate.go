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
func AggregateDetail(ctx context.Context, records []MergedRecord, now time.Time) (DetailedMetrics, error) {
	if len(records) == 0 {
		return DetailedMetrics{}, nil
	}

	// Sort records chronologically once so timelines are ordered.
	sorted := make([]MergedRecord, len(records))
	copy(sorted, records)
	slices.SortStableFunc(sorted, func(a, b MergedRecord) int {
		aTs, bTs := recordSortTs(a), recordSortTs(b)
		if c := aTs.Compare(bTs); c != 0 {
			return c
		}
		if c := cmp.Compare(a.SessionID, b.SessionID); c != 0 {
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

// recordSortTs returns the best timestamp for sorting a MergedRecord.
func recordSortTs(r MergedRecord) time.Time {
	switch r.Kind {
	case RecordKindPrompt:
		return r.PromptTs
	case RecordKindToolUse:
		if !r.PreToolTs.IsZero() {
			return r.PreToolTs
		}
	case RecordKindToolResult:
		if !r.PostToolTs.IsZero() {
			return r.PostToolTs
		}
	case RecordKindAgentSpawn, RecordKindStop, RecordKindHookEvent:
		if !r.PreToolTs.IsZero() {
			return r.PreToolTs
		}
	}
	// Fallback: use any non-zero timestamp.
	for _, t := range []time.Time{r.PromptTs, r.PreToolTs, r.PostToolTs, r.CreatedAt} {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

// processRecord folds one record into the aggregation state.
func processRecord(st *aggState, r MergedRecord) {
	ts := recordSortTs(r)
	st.hours[ts.Truncate(time.Hour)]++
	toolName := r.ToolName
	// Re-key shell tool calls into derived per-command buckets.
	if isToolName(toolName, apmconfig.ToolShell) && (r.Kind == RecordKindToolUse || r.Kind == RecordKindToolResult) {
		toolName = classifyShell(r.ToolInput, r.Cwd)
	}
	s := touchSessionState(st, r)
	switch r.Kind {
	case RecordKindPrompt:
		processPromptRecord(st, s, r)
	case RecordKindToolUse:
		processToolUseRecord(st, s, r, toolName, ts)
	case RecordKindToolResult:
		resolveToolResult(st, s, r)
	case RecordKindAssistantText:
		s.assistantResponse = truncateUTF8(r.AssistantText, maxAssistantResponseLength)
	case RecordKindSessionMeta:
		processSessionMetaRecord(st, s, r)
	case RecordKindAgentSpawn:
		s.timeline = append(s.timeline, EventEntry{Ts: ts, Event: apmconfig.EventAgentSpawn})
	case RecordKindStop:
		s.stopped = true
		s.timeline = append(s.timeline, EventEntry{Ts: ts, Event: apmconfig.EventStop})
		if r.AssistantText != "" {
			s.assistantResponse = truncateUTF8(r.AssistantText, maxAssistantResponseLength)
		}
	case RecordKindHookEvent:
		s.timeline = append(s.timeline, EventEntry{
			Ts:           ts,
			Event:        r.EventName,
			InputSummary: inputSummary(r.ToolInput, r.EventName, s.cwd),
			ToolInput:    formatToolInput(r.ToolInput),
			ToolResult:   r.ToolResult,
		})
	}
}

func processSessionMetaRecord(st *aggState, s *sessionState, r MergedRecord) {
	s.totalInputTokens += r.TotalInputTokens
	s.totalOutputTokens += r.TotalOutputTokens
	s.totalCredits += r.TotalCredits
	if r.Title != "" && s.sumTitle == "" && len(s.prompts) == 0 {
		s.sumTitle = r.Title
	}
	s.toolCalls += r.ToolCalls
	s.prompts = append(s.prompts, r.PromptTexts...)
}

func processToolUseRecord(st *aggState, s *sessionState, r MergedRecord, toolName string, ts time.Time) {
	s.toolCalls++
	summary := inputSummary(r.ToolInput, toolName, s.cwd)
	entry := EventEntry{Ts: ts, Event: apmconfig.EventPreToolUse, Tool: toolName, InputSummary: summary, ToolInput: formatToolInput(r.ToolInput), toolUseID: r.ToolUseID}
	s.timeline = append(s.timeline, entry)

	if r.SubAgent != nil {
		s.subAgentCalls = append(s.subAgentCalls, *r.SubAgent)
	}
	if len(r.SubAgents) > 0 {
		s.subAgentCalls = append(s.subAgentCalls, r.SubAgents...)
	}

	if r.ToolUseID != "" {
		if s.pendingToolUse == nil {
			s.pendingToolUse = make(map[string]int)
		}
		s.pendingToolUse[r.ToolUseID] = len(s.timeline) - 1
	}

	if toolName == "summary" {
		if td := extractSummaryTitle(r.ToolInput); td != "" {
			s.sumTitle = td
		}
	}
	td := toolEntryForCall(st.tools, toolName)
	td.CallCount++
	recordToolAliasCall(td, toolName)

	if isToolName(toolName, apmconfig.ToolRead) && len(r.ToolInput) > 0 {
		if match := skillPathRe.FindSubmatch(r.ToolInput); match != nil {
			st.skills[string(match[1])]++
		}
	}

	if r.ActionState == "Rejected" || r.ActionState == "Error" {
		return
	}
	if isWriteChangeTool(toolName) {
		if fc, ok := parseWriteInput(r.ToolInput, ts, s.cwd); ok {
			s.changes = append(s.changes, fc)
		} else if fc, ok := parseIDEFileChange(r.ToolInput, toolName, ts, s.cwd); ok {
			s.changes = append(s.changes, fc)
		}
	}
	if toolName == ActionCreate || toolName == ActionDelete {
		if fc, ok := parseIDEFileChange(r.ToolInput, toolName, ts, s.cwd); ok {
			s.changes = append(s.changes, fc)
		}
	}
}

func isWriteChangeTool(toolName string) bool {
	return isToolName(toolName, apmconfig.ToolWrite)
}

func processPromptRecord(st *aggState, s *sessionState, r MergedRecord) {
	s.timeline = append(s.timeline, EventEntry{Ts: r.PromptTs, Event: apmconfig.EventUserPromptSubmit})
	s.prompts = append(s.prompts, r.PromptText)
	s.assistantResponses = append(s.assistantResponses, r.TurnResponse)
}

// resolveToolResult finds the matching toolUse in the timeline by ToolUseID
// and computes duration and error status.
func resolveToolResult(st *aggState, s *sessionState, r MergedRecord) {
	if r.ToolUseID == "" {
		return
	}
	matchIdx, ok := s.pendingToolUse[r.ToolUseID]
	if !ok {
		return
	}
	delete(s.pendingToolUse, r.ToolUseID)

	preTs := s.timeline[matchIdx].Ts
	postTs := r.PostToolTs
	if !postTs.IsZero() && !preTs.IsZero() {
		s.timeline[matchIdx].Duration = JSONDuration(postTs.Sub(preTs))
	}
	if r.ToolResult != "" {
		s.timeline[matchIdx].ToolResult = r.ToolResult
		if s.timeline[matchIdx].InputSummary == "" {
			s.timeline[matchIdx].InputSummary = cleanSummary(r.ToolResult)
		}
	}
	appendResolvedSubAgents(s, r, preTs, s.timeline[matchIdx].Duration)

	td := toolEntryForCall(st.tools, s.timeline[matchIdx].Tool)
	call := ToolCall{
		Ts: preTs, Session: r.SessionID, Agent: s.agent, Tool: s.timeline[matchIdx].Tool,
		Duration: s.timeline[matchIdx].Duration, InputSummary: s.timeline[matchIdx].InputSummary,
		ToolInput: s.timeline[matchIdx].ToolInput,
	}

	if r.ToolStatus == ToolStatusError {
		s.timeline[matchIdx].IsError = true
		s.timeline[matchIdx].ErrorDetail = r.ErrorDetail
		s.timeline[matchIdx].ErrorDetail = truncateUTF8(s.timeline[matchIdx].ErrorDetail, maxErrorDetailLength)
		call.IsError = true
		td.ErrorCount++
		recordToolAliasError(td, s.timeline[matchIdx].Tool)
		td.Errors = append(td.Errors, call)
	} else {
		s.timeline[matchIdx].matched = true
		td.RecentCalls = append(td.RecentCalls, call)
	}
}

func appendResolvedSubAgents(s *sessionState, r MergedRecord, ts time.Time, dur JSONDuration) {
	if r.SubAgent != nil {
		call := *r.SubAgent
		if call.Ts.IsZero() {
			call.Ts = ts
		}
		if call.Duration == 0 {
			call.Duration = dur
		}
		s.subAgentCalls = append(s.subAgentCalls, call)
	}
	for _, call := range r.SubAgents {
		if call.Ts.IsZero() {
			call.Ts = ts
		}
		if call.Duration == 0 {
			call.Duration = dur
		}
		s.subAgentCalls = append(s.subAgentCalls, call)
	}
}

// finalizeSessionStats marks unmatched preToolUse as errors, builds per-session
// details, and folds them into agent stats.
func finalizeSessionStats(st *aggState) {
	absorbFirstPromptOnlyAgentSplits(st)
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
	var active bool
	if s.stopped {
		active = false
	} else {
		active = now.Sub(s.end) <= activeSessionTimeout
	}
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
		FilesChanged:     s.filesChangedCached,
		TotalInputTokens: s.totalInputTokens, TotalOutputTokens: s.totalOutputTokens, TotalCredits: s.totalCredits,
	}
}

// markUnmatchedToolCalls walks timelines and marks unresolved preToolUse entries as errors.
func markUnmatchedToolCalls(st *aggState) {
	for _, s := range st.sessions {
		for i := range s.timeline {
			ev := &s.timeline[i]
			if ev.Event != apmconfig.EventPreToolUse {
				continue
			}
			if ev.matched || ev.IsError {
				continue
			}
			// Unmatched toolUse → error
			ev.IsError = true
			td := toolEntryForCall(st.tools, ev.Tool)
			td.ErrorCount++
			recordToolAliasError(td, ev.Tool)
			td.Errors = append(td.Errors, ToolCall{
				Ts: ev.Ts, Session: s.id, Agent: s.agent, Tool: ev.Tool, IsError: true,
				InputSummary: ev.InputSummary, ToolInput: ev.ToolInput,
			})
		}
	}
}

func absorbFirstPromptOnlyAgentSplits(st *aggState) {
	bySession := map[string][]*sessionState{}
	for _, s := range st.sessions {
		bySession[s.id] = append(bySession[s.id], s)
	}
	for _, states := range bySession {
		if len(states) != 2 {
			continue
		}
		from, to := firstPromptOnlySplit(states[0], states[1])
		if from == nil || to == nil {
			continue
		}
		mergeFirstPromptOnlyState(to, from)
		delete(st.sessions, compositeKey(from.id, from.agent))
	}
}

func firstPromptOnlySplit(a, b *sessionState) (*sessionState, *sessionState) {
	switch {
	case isFirstPromptOnlyAgentState(a, b):
		return a, b
	case isFirstPromptOnlyAgentState(b, a):
		return b, a
	default:
		return nil, nil
	}
}

func isFirstPromptOnlyAgentState(candidate, target *sessionState) bool {
	if len(candidate.prompts) != 1 || candidate.toolCalls != 0 || candidate.stopped {
		return false
	}
	if !hasNonPromptActivity(target) {
		return false
	}
	if !hasOnlyPromptTimeline(candidate.timeline) {
		return false
	}
	if len(candidate.changes) != 0 || len(candidate.subAgentCalls) != 0 || len(candidate.pendingToolUse) != 0 {
		return false
	}
	if candidate.start.IsZero() || target.start.IsZero() || !candidate.start.Before(target.start) {
		return false
	}
	return true
}

func hasNonPromptActivity(s *sessionState) bool {
	if s.toolCalls > 0 || s.stopped || s.assistantResponse != "" {
		return true
	}
	if len(s.changes) > 0 || len(s.subAgentCalls) > 0 || len(s.pendingToolUse) > 0 {
		return true
	}
	for _, response := range s.assistantResponses {
		if response != "" {
			return true
		}
	}
	for _, ev := range s.timeline {
		if ev.Event != apmconfig.EventUserPromptSubmit {
			return true
		}
	}
	return false
}

func hasOnlyPromptTimeline(timeline []EventEntry) bool {
	if len(timeline) == 0 {
		return true
	}
	return len(timeline) == 1 && timeline[0].Event == apmconfig.EventUserPromptSubmit
}

func mergeFirstPromptOnlyState(dst, src *sessionState) {
	if dst.start.IsZero() || (!src.start.IsZero() && src.start.Before(dst.start)) {
		dst.start = src.start
	}
	if src.end.After(dst.end) {
		dst.end = src.end
	}
	if dst.cwd == "" {
		dst.cwd = src.cwd
	}
	if dst.sumTitle == "" {
		dst.sumTitle = src.sumTitle
	}
	dst.prompts = append(slices.Clone(src.prompts), dst.prompts...)
	dst.assistantResponses = append(slices.Clone(src.assistantResponses), dst.assistantResponses...)
	if dst.assistantResponse == "" {
		dst.assistantResponse = src.assistantResponse
	}
	dst.timeline = append(dst.timeline, src.timeline...)
	slices.SortStableFunc(dst.timeline, func(a, b EventEntry) int { return a.Ts.Compare(b.Ts) })
	dst.totalInputTokens += src.totalInputTokens
	dst.totalOutputTokens += src.totalOutputTokens
	dst.totalCredits += src.totalCredits
}

// buildSessionDetails builds per-session SessionDetail entries and appends to st.sessionDetails.
func buildSessionDetails(st *aggState) {
	for _, s := range st.sessions {
		base := newSessionMetric(s, st.now)
		prompts := slices.Clone(s.prompts)
		if prompts == nil {
			prompts = []string{}
		}
		var changes []FileChange
		if len(s.changes) > 0 {
			changes = slices.Clone(s.changes)
			slices.SortStableFunc(changes, func(a, b FileChange) int { return a.Ts.Compare(b.Ts) })
		}
		st.sessionDetails = append(st.sessionDetails, SessionDetail{
			SessionMetric:      base,
			PromptHistory:      prompts,
			Timeline:           s.timeline,
			ToolSummary:        sessionToolSummary(s.timeline),
			AssistantResponse:  s.assistantResponse,
			AssistantResponses: slices.Clone(s.assistantResponses),
			Changes:            changes,
			SubAgentCalls:      s.subAgentCalls,
		})
	}
}

// sessionToolSummary computes per-tool stats from a session timeline.
// Returns nil if there are no tool calls.
func sessionToolSummary(timeline []EventEntry) []SessionToolSummary {
	m := map[string]*toolAgg{}
	for _, ev := range timeline {
		if ev.Event != apmconfig.EventPreToolUse {
			continue
		}
		a := m[ev.Tool]
		if a == nil {
			a = &toolAgg{}
			m[ev.Tool] = a
		}
		a.addCall(ev.IsError, time.Duration(ev.Duration))
	}
	return finalizeToolAgg(m)
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
		a.TotalInputTokens += s.totalInputTokens
		a.TotalOutputTokens += s.totalOutputTokens
		a.TotalCredits += s.totalCredits
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
			ta.addCall(ev.IsError, time.Duration(ev.Duration))
			if ev.IsError {
				a.ToolErrorCnt++
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
		out := finalizeToolAgg(agentTools[name])
		if out == nil {
			out = []SessionToolSummary{}
		}
		a.ToolSummary = out
	}
}

// finalizeToolDetails computes ErrorRate/AvgDuration and sorts
// RecentCalls/Errors newest-first with a per-slice cap.
func finalizeToolDetails(td *ToolDetail) {
	if td.CallCount > 0 {
		td.ErrorRate = float64(td.ErrorCount) / float64(td.CallCount)
		for i := range td.Aliases {
			td.Aliases[i].Percentage = float64(td.Aliases[i].CallCount) / float64(td.CallCount)
		}
	}
	slices.SortFunc(td.Aliases, func(a, b ToolAliasMetric) int {
		if a.CallCount != b.CallCount {
			return cmp.Compare(b.CallCount, a.CallCount)
		}
		return cmp.Compare(a.Name, b.Name)
	})
	if len(td.RecentCalls) > 0 {
		var total time.Duration
		var count int
		for _, c := range td.RecentCalls {
			if c.Duration <= 0 {
				continue
			}
			total += time.Duration(c.Duration)
			count++
		}
		if count > 0 {
			td.AvgDuration = JSONDuration(total / time.Duration(count))
		}
		slices.SortFunc(td.RecentCalls, sortToolCallByTsDesc)
		if len(td.RecentCalls) > maxRecentCalls {
			td.RecentCalls = td.RecentCalls[:maxRecentCalls]
		}
	}
	slices.SortFunc(td.Errors, sortToolCallByTsDesc)
	if len(td.Errors) > maxErrors {
		td.Errors = td.Errors[:maxErrors]
	}
}

func toolEntryForCall(tools map[string]*ToolDetail, rawName string) *ToolDetail {
	return toolEntry(tools, CanonicalToolNameForAggregation(rawName))
}

func recordToolAliasCall(td *ToolDetail, rawName string) {
	if td == nil {
		return
	}
	for i := range td.Aliases {
		if td.Aliases[i].Name == rawName {
			td.Aliases[i].CallCount++
			return
		}
	}
	td.Aliases = append(td.Aliases, ToolAliasMetric{Name: rawName, CallCount: 1})
}

func recordToolAliasError(td *ToolDetail, rawName string) {
	if td == nil {
		return
	}
	for i := range td.Aliases {
		if td.Aliases[i].Name == rawName {
			td.Aliases[i].ErrorCount++
			return
		}
	}
	td.Aliases = append(td.Aliases, ToolAliasMetric{Name: rawName, ErrorCount: 1})
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
	slices.SortFunc(overview.Tools, sortToolMetricByCallCountDescNameAsc)
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
	slices.SortFunc(toolDetails, sortToolDetailByCallCountDescNameAsc)

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
			td := toolEntryForCall(tools, ev.Tool)
			td.CallCount++
			recordToolAliasCall(td, ev.Tool)
			call := ToolCall{
				Ts: ev.Ts, Session: sd.ID, Agent: sd.Agent, Tool: ev.Tool,
				Duration: ev.Duration, IsError: ev.IsError, InputSummary: ev.InputSummary,
				ToolInput: ev.ToolInput,
			}
			if ev.IsError {
				td.ErrorCount++
				recordToolAliasError(td, ev.Tool)
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
	slices.SortFunc(details, sortToolDetailByCallCountDescNameAsc)
	slices.SortFunc(metrics, sortToolMetricByCallCountDescNameAsc)
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
	slices.SortFunc(keys, sortTimeAsc)
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
