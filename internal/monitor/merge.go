package monitor

import (
	"bufio"
	"cmp"
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

// AgentRef names an agent that participated in a session. URL construction
// lives in the Web layer; this package stays URL-free.
type AgentRef struct {
	Agent    string
	AgentKey string
}

// MergeSessionDetails merges all SessionDetail entries sharing the same ID
// into a single synthetic SessionDetail and returns the agents participating
// in the sid (in input order). Entries with a differing ID are ignored.
//
// Merge rules (see plan §D2):
//   - ID        = first element's ID
//   - Agent     = "(all)"
//   - AgentKey  = ID + "|(all)"
//   - Title     = first non-empty Title by LastActivity desc
//   - StartTime = min, EndTime / LastActivity = max
//   - Duration  = EndTime - StartTime
//   - Active    = any input Active
//   - ToolCalls = sum, Prompts = sum
//   - Cwd       = most recent (by LastActivity desc) non-empty Cwd
//   - PromptHistory = concat of all inputs' PromptHistory (each is newest
//     first per-agent), globally sorted newest-first by paired timestamp
//   - Timeline  = concat of all Timelines, stably sorted by Ts ascending
func MergeSessionDetails(details []SessionDetail) (SessionDetail, []AgentRef) {
	if len(details) == 0 {
		return SessionDetail{}, nil
	}
	id := details[0].ID
	src := make([]SessionDetail, 0, len(details))
	for _, sd := range details {
		if sd.ID == id {
			src = append(src, sd)
		}
	}
	if len(src) == 0 {
		return SessionDetail{}, nil
	}
	start, end, lastActivity := mergeSessionTimestamps(src)
	merged := SessionDetail{SessionMetric: SessionMetric{
		ID: id, Agent: "(all)", AgentKey: id + "|(all)",
		StartTime: start, EndTime: end, LastActivity: lastActivity,
		Duration: JSONDuration(end.Sub(start)),
	}}
	refs := make([]AgentRef, 0, len(src))
	for _, sd := range src {
		refs = append(refs, AgentRef{Agent: sd.Agent, AgentKey: sd.AgentKey})
		merged.ToolCalls += sd.ToolCalls
		merged.Prompts += sd.Prompts
		merged.TotalInputTokens += sd.TotalInputTokens
		merged.TotalOutputTokens += sd.TotalOutputTokens
		merged.TotalCredits += sd.TotalCredits
		if sd.Active {
			merged.Active = true
		}
	}
	merged.Title, merged.Cwd = mergeSessionTitleCwd(src)
	merged.Timeline, merged.Changes = mergeSessionContent(src)
	merged.FilesChanged = countUniqueFiles(merged.Changes)
	merged.PromptHistory, merged.AssistantResponses = pairPromptsWithTimeline(src)
	merged.ToolSummary = mergeToolSummary(src)
	merged.AssistantResponse = mergeAssistantResponse(src)
	merged.SubAgentCalls = mergeSubAgentCalls(src)
	return merged, refs
}

// mergeSessionTimestamps returns the min StartTime, max EndTime, and max LastActivity across all details.
func mergeSessionTimestamps(src []SessionDetail) (start, end, lastActivity time.Time) {
	start, end, lastActivity = src[0].StartTime, src[0].EndTime, src[0].LastActivity
	for _, sd := range src[1:] {
		if sd.StartTime.Before(start) {
			start = sd.StartTime
		}
		if sd.EndTime.After(end) {
			end = sd.EndTime
		}
		if sd.LastActivity.After(lastActivity) {
			lastActivity = sd.LastActivity
		}
	}
	return
}

// mergeSessionTitleCwd picks Title and Cwd from the detail with the latest LastActivity.
func mergeSessionTitleCwd(src []SessionDetail) (title, cwd string) {
	byActivity := slices.Clone(src)
	slices.SortStableFunc(byActivity, func(a, b SessionDetail) int { return b.LastActivity.Compare(a.LastActivity) })
	for _, sd := range byActivity {
		if title == "" && sd.Title != "" {
			title = sd.Title
		}
		if cwd == "" && sd.Cwd != "" {
			cwd = sd.Cwd
		}
		if title != "" && cwd != "" {
			break
		}
	}
	return
}

// mergeSessionContent concatenates and sorts Timeline and FileChanges across all details.
func mergeSessionContent(src []SessionDetail) (timeline []EventEntry, changes []FileChange) {
	for _, sd := range src {
		timeline = append(timeline, sd.Timeline...)
		changes = append(changes, sd.Changes...)
	}
	slices.SortStableFunc(timeline, func(a, b EventEntry) int { return a.Ts.Compare(b.Ts) })
	if len(changes) > 0 {
		slices.SortStableFunc(changes, func(a, b FileChange) int { return a.Ts.Compare(b.Ts) })
	}
	return
}

func mergeSubAgentCalls(src []SessionDetail) []SubAgentCall {
	var calls []SubAgentCall
	for _, sd := range src {
		calls = append(calls, sd.SubAgentCalls...)
	}
	if len(calls) > 0 {
		slices.SortStableFunc(calls, func(a, b SubAgentCall) int { return a.Ts.Compare(b.Ts) })
	}
	return calls
}

// pairPromptsWithTimeline pairs each prompt in history (oldest-first) with
// its timestamp from the timeline (oldest-first) by matching
// EventUserPromptSubmit entries positionally. Unmapped prompts get zero ts.
// Returns prompts and paired responses sorted oldest-first by paired timestamp.
func pairPromptsWithTimeline(details []SessionDetail) ([]string, []string) {
	type tsPrompt struct {
		ts   int64
		seq  int
		p    string
		resp string
	}
	var prompts []tsPrompt

	for _, sd := range details {
		ph := sd.PromptHistory
		if len(ph) == 0 {
			continue
		}
		i := 0
		for _, ev := range sd.Timeline {
			if ev.Event != apmconfig.EventUserPromptSubmit {
				continue
			}
			if i >= len(ph) {
				break
			}
			var resp string
			if i < len(sd.AssistantResponses) {
				resp = sd.AssistantResponses[i]
			}
			prompts = append(prompts, tsPrompt{ts: ev.Ts.UnixNano(), seq: len(prompts), p: ph[i], resp: resp})
			i++
		}
		for ; i < len(ph); i++ {
			var resp string
			if i < len(sd.AssistantResponses) {
				resp = sd.AssistantResponses[i]
			}
			prompts = append(prompts, tsPrompt{ts: 0, seq: len(prompts), p: ph[i], resp: resp})
		}
	}

	slices.SortStableFunc(prompts, func(a, b tsPrompt) int {
		if c := cmp.Compare(a.ts, b.ts); c != 0 {
			return c
		}
		return cmp.Compare(a.seq, b.seq)
	})

	outP := make([]string, len(prompts))
	outR := make([]string, len(prompts))
	for i, p := range prompts {
		outP[i] = p.p
		outR[i] = p.resp
	}
	return outP, outR
}

// mergeToolSummary groups ToolSummary entries by Tool across all agents,
// sums counts, recomputes SuccessRate and weighted AvgDuration, sorts by CallCount desc.
func mergeToolSummary(src []SessionDetail) []SessionToolSummary {
	m := map[string]*toolAgg{}
	for _, sd := range src {
		for _, ts := range sd.ToolSummary {
			a := m[ts.Tool]
			if a == nil {
				a = &toolAgg{}
				m[ts.Tool] = a
			}
			a.addSummary(ts)
		}
	}
	return finalizeToolAgg(m)
}

// mergeAssistantResponse concatenates non-empty AssistantResponse values.
// Single agent: no prefix. Multiple: "[agent-name]\n<response>" separated by "\n\n---\n\n".
// Capped at 2048 bytes.
func mergeAssistantResponse(src []SessionDetail) string {
	type entry struct {
		agent string
		resp  string
	}
	var parts []entry
	for _, sd := range src {
		if sd.AssistantResponse != "" {
			parts = append(parts, entry{agent: sd.Agent, resp: sd.AssistantResponse})
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		r := parts[0].resp
		r = truncateUTF8(r, maxAssistantResponseLength)
		return r
	}
	var sb strings.Builder
	for i, p := range parts {
		if i > 0 {
			sb.WriteString("\n\n---\n\n")
		}
		sb.WriteString("[")
		sb.WriteString(p.agent)
		sb.WriteString("]\n")
		sb.WriteString(p.resp)
	}
	out := sb.String()
	out = truncateUTF8(out, maxAssistantResponseLength)
	return out
}

// HookRecord is one line of a hook log in the new minimal format.
type HookRecord struct {
	Ts              time.Time `json:"ts"`
	Session         string    `json:"session,omitempty"`
	Event           string    `json:"event,omitempty"`
	Agent           string    `json:"agent,omitempty"`
	Tool            string    `json:"tool,omitempty"`
	ShellExitStatus string    `json:"shell_exit_status,omitempty"`
}

// MergedRecord combines sessions (primary) and hook log (supplementary) data
// for one event.
type MergedRecord struct {
	// sessions-derived
	SessionID     string
	Kind          string // "prompt", "toolUse", "toolResult", "assistantText"
	EventName     string // raw lifecycle hook event name for Kind=="hookEvent"
	ToolUseID     string // toolUse/toolResult pairing
	ToolName      string
	ToolInput     json.RawMessage // raw JSON from toolUse.input
	ToolStatus    string          // toolResult status ("success"/"error")
	ErrorDetail   string          // toolResult status=="error": content[0].data
	ToolResult    string          // toolResult content/output when available
	ActionState   string          // IDE action state, when available
	PromptText    string
	AssistantText string
	TurnResponse  string    // final assistant text for the preceding turn (file-order)
	PromptTs      time.Time // Prompt.meta.timestamp (unix seconds)

	// hook-supplemented (zero if no hook match)
	PreToolTs       time.Time
	PostToolTs      time.Time
	Agent           string
	ShellExitStatus string

	// sessions meta-derived
	Title     string
	Cwd       string
	CreatedAt time.Time
	UpdatedAt time.Time

	// sessions meta-derived (token/credit totals, emitted once per session as Kind=="sessionMeta")
	TotalInputTokens  int
	TotalOutputTokens int
	TotalCredits      float64
	// IDE-only: pre-aggregated counts (CLI derives these from individual records)
	PromptTexts []string
	ToolCalls   int
	// sub-agent invocation data (IDE invokeSubAgent or CLI use_subagent records)
	SubAgent  *SubAgentCall
	SubAgents []SubAgentCall
}

// parseHookRecords reads hook JSONL in the new minimal format.
// Lines that don't parse as HookRecord (old format) are silently skipped.
func parseHookRecords(path string) ([]HookRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var recs []HookRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
	var parseFailed int
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec HookRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			parseFailed++ // garbage JSON — will Warn
			continue
		}
		if rec.Ts.IsZero() {
			continue // structurally-valid old-format — silent forward-compat skip
		}
		recs = append(recs, rec)
	}
	if parseFailed > 0 {
		slog.Warn("skipped malformed hook log lines", "path", path, "count", parseFailed)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return recs, nil
}

// decodeOrSkip unmarshals data into T. On failure it logs a slog.Warn with
// session context and returns the zero value of T plus false.
func decodeOrSkip[T any](data json.RawMessage, kind, sessionID string) (T, bool) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		slog.Warn("skipped malformed merge record", "kind", kind, "session", sessionID, "err", err)
		return v, false
	}
	return v, true
}

// extractTextContent unmarshals a text ContentItem. Returns ("", false) if
// the item is not ContentKindText or unmarshaling fails.
func extractTextContent(ci ContentItem) (string, bool) {
	if ci.Kind != ContentKindText {
		return "", false
	}
	var t string
	if err := json.Unmarshal(ci.Data, &t); err != nil {
		return "", false
	}
	return t, true
}

// MergeSessions merges ParsedSession slices with hook log records into
// MergedRecord slices. When hookLogs is nil or empty, sessions-only mode is
// used and hook fields are zero values.
func MergeSessions(sessions []ParsedSession, hookLogs []HookRecord) []MergedRecord {
	hookBySession := make(map[string][]HookRecord, len(hookLogs))
	var unknownHooks []HookRecord
	for _, h := range hookLogs {
		if isUnknownHookSession(h.Session) {
			unknownHooks = append(unknownHooks, h)
			continue
		}
		hookBySession[h.Session] = append(hookBySession[h.Session], h)
	}
	unknownUsed := make([]bool, len(unknownHooks))

	var out []MergedRecord
	for _, s := range sessions {
		meta := s.Meta
		createdAt := time.Time(meta.CreatedAt)
		updatedAt := time.Time(meta.UpdatedAt)

		sessionHooks := hookBySession[meta.SessionID]
		if len(unknownHooks) > 0 {
			sessionHooks = append(sessionHooks, matchUnknownHooks(s, unknownHooks, unknownUsed)...)
		}
		preHooks, postHooks, spawnHooks, stopHooks := groupHooksByEvent(sessionHooks)
		currentAgent := resolveInitialAgent(meta, sessionHooks)
		turnResponses := collectTurnResponses(s.Messages, meta.SessionID)
		inputTok, outputTok, credits := aggregateTokenCredits(meta)

		if inputTok > 0 || outputTok > 0 || credits > 0 {
			out = append(out, MergedRecord{
				SessionID:         meta.SessionID,
				Kind:              RecordKindSessionMeta,
				Agent:             currentAgent,
				Title:             meta.Title,
				Cwd:               meta.Cwd,
				CreatedAt:         createdAt,
				UpdatedAt:         updatedAt,
				TotalInputTokens:  inputTok,
				TotalOutputTokens: outputTok,
				TotalCredits:      credits,
			})
		}

		sessionStart := len(out)
		out = processSessionMessages(s.Messages, meta, preHooks, spawnHooks, stopHooks, turnResponses, &currentAgent, createdAt, updatedAt, out)
		attachPostToolData(out, postHooks, sessionStart)
	}

	return out
}

// groupHooksByEvent categorizes hooks into pre, post, spawn, and stop slices.
func groupHooksByEvent(hooks []HookRecord) (pre, post, spawn, stop []HookRecord) {
	for _, h := range hooks {
		switch h.Event {
		case apmconfig.EventPreToolUse:
			pre = append(pre, h)
		case apmconfig.EventPostToolUse:
			post = append(post, h)
		case apmconfig.EventAgentSpawn:
			spawn = append(spawn, h)
		case apmconfig.EventStop:
			stop = append(stop, h)
		}
	}
	return
}

const unknownHookMatchSlack = 5 * time.Second

func isUnknownHookSession(sessionID string) bool {
	return strings.HasPrefix(sessionID, "unknown-")
}

func matchUnknownHooks(session ParsedSession, unknownHooks []HookRecord, used []bool) []HookRecord {
	toolNames := sessionToolNames(session.Messages, session.Meta.SessionID)
	if len(toolNames) == 0 {
		return nil
	}
	createdAt := time.Time(session.Meta.CreatedAt)
	updatedAt := time.Time(session.Meta.UpdatedAt)
	if createdAt.IsZero() || updatedAt.IsZero() || updatedAt.Before(createdAt) {
		return nil
	}
	start := createdAt.Add(-unknownHookMatchSlack)
	end := updatedAt.Add(unknownHookMatchSlack)

	var matched []HookRecord
	searchAfter := start
	for _, toolName := range toolNames {
		preIdx := findUnknownHook(unknownHooks, used, apmconfig.EventPreToolUse, toolName, searchAfter, end)
		if preIdx < 0 {
			continue
		}
		used[preIdx] = true
		preHook := unknownHooks[preIdx]
		matched = append(matched, preHook)

		postIdx := findUnknownHook(unknownHooks, used, apmconfig.EventPostToolUse, toolName, preHook.Ts, end)
		if postIdx >= 0 {
			used[postIdx] = true
			matched = append(matched, unknownHooks[postIdx])
		}
		searchAfter = preHook.Ts
	}
	return matched
}

func sessionToolNames(messages []SessionMessage, sessionID string) []string {
	var names []string
	for _, msg := range messages {
		if msg.Kind != MessageKindAssistantMessage {
			continue
		}
		ad, ok := decodeOrSkip[AssistantData](msg.Data, "assistantMessage:toolNames", sessionID)
		if !ok {
			continue
		}
		for _, ci := range ad.Content {
			if ci.Kind != ContentKindToolUse {
				continue
			}
			tu, ok := decodeOrSkip[ToolUseData](ci.Data, "toolUse:toolNames", sessionID)
			if ok && tu.Name != "" {
				names = append(names, tu.Name)
			}
		}
	}
	return names
}

func findUnknownHook(
	hooks []HookRecord,
	used []bool,
	event string,
	toolName string,
	start time.Time,
	end time.Time,
) int {
	bestIdx := -1
	for i, hook := range hooks {
		if used[i] || hook.Event != event || hook.Tool != toolName {
			continue
		}
		if hook.Ts.Before(start) || hook.Ts.After(end) {
			continue
		}
		if bestIdx < 0 || hook.Ts.Before(hooks[bestIdx].Ts) {
			bestIdx = i
		}
	}
	return bestIdx
}

// resolveInitialAgent returns the active agent name for a session.
// Falls back to the first non-empty agent in hook logs when session metadata
// has no agent_name (e.g. sub-agent sessions).
func resolveInitialAgent(meta SessionMeta, hooks []HookRecord) string {
	if meta.SessionState.AgentName != "" {
		return meta.SessionState.AgentName
	}
	for _, h := range hooks {
		if h.Agent != "" {
			return h.Agent
		}
	}
	return ""
}

// collectTurnResponses does a pre-pass over messages to collect per-turn final
// assistant text in file order. For each prompt (except the first), the
// response is the last AssistantMessage text seen before that prompt. The
// final turn's response is the last AssistantMessage text in the file.
func collectTurnResponses(messages []SessionMessage, sessionID string) []string {
	var turnResponses []string
	var lastText string
	firstPrompt := true
	for _, m := range messages {
		switch m.Kind {
		case MessageKindPrompt:
			if firstPrompt {
				firstPrompt = false
			} else {
				turnResponses = append(turnResponses, lastText)
				lastText = ""
			}
		case MessageKindAssistantMessage:
			ad, ok := decodeOrSkip[AssistantData](m.Data, "assistantMessage:prepass", sessionID)
			if !ok {
				continue
			}
			for _, ci := range ad.Content {
				if t, ok := extractTextContent(ci); ok {
					lastText = t
				}
			}
		}
	}
	if !firstPrompt {
		turnResponses = append(turnResponses, lastText)
	}
	return turnResponses
}

// aggregateTokenCredits sums token counts and credits from per-turn metadata.
func aggregateTokenCredits(meta SessionMeta) (inputTok, outputTok int, credits float64) {
	for _, utm := range meta.SessionState.ConversationMetadata.UserTurnMetadatas {
		inputTok += utm.InputTokenCount
		outputTok += utm.OutputTokenCount
		for _, mu := range utm.MeteringUsage {
			credits += mu.Value
		}
	}
	return
}

// messageProcessorState holds mutable state shared across message-kind handlers.
type messageProcessorState struct {
	meta          SessionMeta
	preHooks      []HookRecord
	turnResponses []string
	currentAgent  *string
	createdAt     time.Time
	updatedAt     time.Time
	toolUseIdx    int
	promptIdx     int
	subAgentsByID map[string][]SubAgentCall
}

// base returns the common MergedRecord fields.
func (s *messageProcessorState) base() MergedRecord {
	return MergedRecord{
		SessionID: s.meta.SessionID,
		Agent:     *s.currentAgent,
		Title:     s.meta.Title,
		Cwd:       s.meta.Cwd,
		CreatedAt: s.createdAt,
		UpdatedAt: s.updatedAt,
	}
}

func (s *messageProcessorState) processPrompt(msg SessionMessage, out []MergedRecord) []MergedRecord {
	pd, ok := decodeOrSkip[PromptData](msg.Data, RecordKindPrompt, s.meta.SessionID)
	if !ok {
		return out
	}
	promptTs := time.Unix(pd.Meta.Timestamp, 0).UTC()
	var sb strings.Builder
	for _, ci := range pd.Content {
		if ci.Kind == ContentKindText {
			var t string
			if err := json.Unmarshal(ci.Data, &t); err == nil {
				sb.WriteString(t)
			}
		}
	}
	var turnResp string
	if s.promptIdx < len(s.turnResponses) {
		turnResp = s.turnResponses[s.promptIdx]
	}
	s.promptIdx++
	rec := s.base()
	rec.Kind = RecordKindPrompt
	rec.PromptText = sb.String()
	rec.TurnResponse = turnResp
	rec.PromptTs = promptTs
	return append(out, rec)
}

func (s *messageProcessorState) processAssistantMessage(msg SessionMessage, out []MergedRecord) []MergedRecord {
	ad, ok := decodeOrSkip[AssistantData](msg.Data, "assistantMessage", s.meta.SessionID)
	if !ok {
		return out
	}
	for _, ci := range ad.Content {
		switch ci.Kind {
		case ContentKindText:
			if t, ok := extractTextContent(ci); ok {
				rec := s.base()
				rec.Kind = RecordKindAssistantText
				rec.AssistantText = t
				out = append(out, rec)
			}
		case ContentKindToolUse:
			tu, ok := decodeOrSkip[ToolUseData](ci.Data, ContentKindToolUse, s.meta.SessionID)
			if !ok {
				continue
			}
			subAgents := parseUseSubagentInput(tu.Input)
			if len(subAgents) > 0 && tu.ToolUseID != "" {
				if s.subAgentsByID == nil {
					s.subAgentsByID = make(map[string][]SubAgentCall)
				}
				s.subAgentsByID[tu.ToolUseID] = subAgents
			}
			var preTs time.Time
			if s.toolUseIdx < len(s.preHooks) {
				preTs = s.preHooks[s.toolUseIdx].Ts
				if s.preHooks[s.toolUseIdx].Agent != "" {
					*s.currentAgent = s.preHooks[s.toolUseIdx].Agent
				}
			}
			rec := s.base()
			rec.Kind = RecordKindToolUse
			rec.ToolUseID = tu.ToolUseID
			rec.ToolName = tu.Name
			rec.ToolInput = tu.Input
			rec.PreToolTs = preTs
			out = append(out, rec)
			s.toolUseIdx++
		}
	}
	return out
}

func (s *messageProcessorState) processToolResults(msg SessionMessage, out []MergedRecord) []MergedRecord {
	trs, ok := decodeOrSkip[struct {
		Content []ContentItem `json:"content"`
	}](msg.Data, "toolResults", s.meta.SessionID)
	if !ok {
		return out
	}
	for _, ci := range trs.Content {
		if ci.Kind != ContentKindToolResult {
			continue
		}
		tr, ok := decodeOrSkip[ToolResultData](ci.Data, ContentKindToolResult, s.meta.SessionID)
		if !ok {
			continue
		}
		var errorDetail string
		if tr.Status == ToolStatusError && len(tr.Content) > 0 {
			var d string
			if err := json.Unmarshal(tr.Content[0].Data, &d); err == nil {
				errorDetail = d
			}
		}
		rec := s.base()
		rec.Kind = RecordKindToolResult
		rec.ToolUseID = tr.ToolUseID
		rec.ToolStatus = tr.Status
		rec.ErrorDetail = errorDetail
		if len(s.subAgentsByID) > 0 {
			if calls, ok := s.subAgentsByID[tr.ToolUseID]; ok {
				rec.SubAgents = attachSubagentResults(calls, tr.Content)
				delete(s.subAgentsByID, tr.ToolUseID)
			}
		}
		out = append(out, rec)
	}
	return out
}

func parseUseSubagentInput(raw json.RawMessage) []SubAgentCall {
	var inp struct {
		Content struct {
			Subagents []struct {
				AgentName       string `json:"agent_name"`
				Query           string `json:"query"`
				RelevantContext string `json:"relevant_context"`
			} `json:"subagents"`
		} `json:"content"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &inp) != nil || len(inp.Content.Subagents) == 0 {
		return nil
	}
	calls := make([]SubAgentCall, 0, len(inp.Content.Subagents))
	for _, sa := range inp.Content.Subagents {
		calls = append(calls, SubAgentCall{
			AgentName:   sa.AgentName,
			Explanation: sa.RelevantContext,
			Prompt:      sa.Query,
		})
	}
	return calls
}

func attachSubagentResults(calls []SubAgentCall, content []ContentItem) []SubAgentCall {
	summaries := subagentResultSummaries(content)
	for i := range calls {
		if i >= len(summaries) {
			continue
		}
		if summaries[i].TaskDescription != "" {
			calls[i].Explanation = summaries[i].TaskDescription
		}
		calls[i].Response = summaries[i].TaskResult
	}
	return calls
}

type subagentSummary struct {
	TaskDescription string `json:"taskDescription"`
	ContextSummary  string `json:"contextSummary"`
	TaskResult      string `json:"taskResult"`
}

func subagentResultSummaries(content []ContentItem) []subagentSummary {
	var summaries []subagentSummary
	for _, ci := range content {
		if ci.Kind != ContentKindJSON {
			continue
		}
		var payload struct {
			Summaries []subagentSummary `json:"summaries"`
		}
		if err := json.Unmarshal(ci.Data, &payload); err != nil {
			continue
		}
		summaries = append(summaries, payload.Summaries...)
	}
	return summaries
}

// processSessionMessages iterates over messages and appends MergedRecords to out.
// currentAgent is a pointer because preHooks update it during iteration.
func processSessionMessages(
	messages []SessionMessage,
	meta SessionMeta,
	preHooks, spawnHooks, stopHooks []HookRecord,
	turnResponses []string,
	currentAgent *string,
	createdAt, updatedAt time.Time,
	out []MergedRecord,
) []MergedRecord {
	s := &messageProcessorState{
		meta: meta, preHooks: preHooks, turnResponses: turnResponses,
		currentAgent: currentAgent, createdAt: createdAt, updatedAt: updatedAt,
	}
	for _, msg := range messages {
		switch msg.Kind {
		case MessageKindPrompt:
			out = s.processPrompt(msg, out)
		case MessageKindAssistantMessage:
			out = s.processAssistantMessage(msg, out)
		case MessageKindToolResults:
			out = s.processToolResults(msg, out)
		}
	}

	for _, h := range spawnHooks {
		rec := s.base()
		rec.Kind = RecordKindAgentSpawn
		rec.PreToolTs = h.Ts
		if h.Agent != "" {
			rec.Agent = h.Agent
		}
		out = append(out, rec)
	}

	for _, h := range stopHooks {
		rec := s.base()
		rec.Kind = RecordKindStop
		rec.PreToolTs = h.Ts
		if h.Agent != "" {
			rec.Agent = h.Agent
		}
		out = append(out, rec)
	}

	return out
}

// attachPostToolData attaches postToolUse data to toolResult records by position.
func attachPostToolData(records []MergedRecord, postHooks []HookRecord, sessionStart int) {
	if len(postHooks) == 0 {
		return
	}
	postIdx := 0
	for i := sessionStart; i < len(records); i++ {
		if records[i].Kind != RecordKindToolResult {
			continue
		}
		if postIdx < len(postHooks) {
			records[i].PostToolTs = postHooks[postIdx].Ts
			if postHooks[postIdx].Agent != "" {
				records[i].Agent = postHooks[postIdx].Agent
			}
			records[i].ShellExitStatus = postHooks[postIdx].ShellExitStatus
			postIdx++
		}
	}
}
