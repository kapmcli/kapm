package monitor

import (
	"bufio"
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

	// Keep only inputs whose ID matches; drop mismatches silently.
	src := make([]SessionDetail, 0, len(details))
	for _, sd := range details {
		if sd.ID == id {
			src = append(src, sd)
		}
	}
	if len(src) == 0 {
		return SessionDetail{}, nil
	}

	merged := SessionDetail{
		SessionMetric: SessionMetric{
			ID:           id,
			Agent:        "(all)",
			AgentKey:     id + "|(all)",
			StartTime:    src[0].StartTime,
			EndTime:      src[0].EndTime,
			LastActivity: src[0].LastActivity,
		},
	}

	refs := make([]AgentRef, 0, len(src))
	var timeline []EventEntry

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
		if sd.StartTime.Before(merged.StartTime) {
			merged.StartTime = sd.StartTime
		}
		if sd.EndTime.After(merged.EndTime) {
			merged.EndTime = sd.EndTime
		}
		if sd.LastActivity.After(merged.LastActivity) {
			merged.LastActivity = sd.LastActivity
		}
		timeline = append(timeline, sd.Timeline...)
	}

	// Title: first non-empty by LastActivity desc.
	byActivity := slices.Clone(src)
	slices.SortStableFunc(byActivity, func(a, b SessionDetail) int { return b.LastActivity.Compare(a.LastActivity) })
	for _, sd := range byActivity {
		if sd.Title != "" {
			merged.Title = sd.Title
			break
		}
	}
	for _, sd := range byActivity {
		if sd.Cwd != "" {
			merged.Cwd = sd.Cwd
			break
		}
	}

	merged.Duration = JSONDuration(merged.EndTime.Sub(merged.StartTime))

	slices.SortStableFunc(timeline, func(a, b EventEntry) int { return a.Ts.Compare(b.Ts) })
	merged.Timeline = timeline

	merged.PromptHistory = pairPromptsWithTimeline(src)

	merged.ToolSummary = mergeToolSummary(src)
	merged.AssistantResponse = mergeAssistantResponse(src)

	var changes []FileChange
	for _, sd := range src {
		changes = append(changes, sd.Changes...)
	}
	if len(changes) > 0 {
		slices.SortStableFunc(changes, func(a, b FileChange) int { return a.Ts.Compare(b.Ts) })
		merged.Changes = changes
	}
	merged.FilesChanged = countUniqueFiles(changes)

	return merged, refs
}

// pairPromptsWithTimeline pairs each prompt in history (oldest-first) with
// its timestamp from the timeline (oldest-first) by matching
// EventUserPromptSubmit entries positionally. Unmapped prompts get zero ts.
// Returns prompts sorted oldest-first by paired timestamp.
func pairPromptsWithTimeline(details []SessionDetail) []string {
	type tsPrompt struct {
		ts  int64
		seq int
		p   string
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
			prompts = append(prompts, tsPrompt{ts: ev.Ts.UnixNano(), seq: len(prompts), p: ph[i]})
			i++
		}
		for ; i < len(ph); i++ {
			prompts = append(prompts, tsPrompt{ts: 0, seq: len(prompts), p: ph[i]})
		}
	}

	slices.SortStableFunc(prompts, func(a, b tsPrompt) int {
		if a.ts != b.ts {
			if a.ts < b.ts {
				return -1
			}
			return 1
		}
		if a.seq < b.seq {
			return -1
		}
		if a.seq > b.seq {
			return 1
		}
		return 0
	})

	out := make([]string, len(prompts))
	for i, p := range prompts {
		out[i] = p.p
	}
	return out
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
	ToolUseID     string // toolUse/toolResult pairing
	ToolName      string
	ToolInput     json.RawMessage // raw JSON from toolUse.input
	ToolStatus    string          // toolResult status ("success"/"error")
	ErrorDetail   string          // toolResult status=="error": content[0].data
	PromptText    string
	AssistantText string
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
	// IDE-only: sub-agent invocation data (set on invokeSubAgent toolUse records)
	SubAgent *SubAgentCall
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
	var skipped int
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec HookRecord
		if err := json.Unmarshal(line, &rec); err != nil || rec.Ts.IsZero() {
			skipped++
			continue // old format or malformed — skip silently
		}
		recs = append(recs, rec)
	}
	if skipped > 0 {
		slog.Debug("skipped hook log lines (old format or malformed)", "count", skipped)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return recs, nil
}

// MergeSessions merges ParsedSession slices with hook log records into
// MergedRecord slices. When hookLogs is nil or empty, sessions-only mode is
// used and hook fields are zero values.
func MergeSessions(sessions []ParsedSession, hookLogs []HookRecord) []MergedRecord {
	// Group hook records by session UUID.
	hookBySession := make(map[string][]HookRecord, len(hookLogs))
	for _, h := range hookLogs {
		hookBySession[h.Session] = append(hookBySession[h.Session], h)
	}

	var out []MergedRecord
	for _, s := range sessions {
		meta := s.Meta
		createdAt := time.Time(meta.CreatedAt)
		updatedAt := time.Time(meta.UpdatedAt)

		sessionHooks := hookBySession[meta.SessionID]
		// Collect preToolUse and postToolUse in order.
		var preHooks, postHooks []HookRecord
		for _, h := range sessionHooks {
			switch h.Event {
			case "preToolUse":
				preHooks = append(preHooks, h)
			case "postToolUse":
				postHooks = append(postHooks, h)
			}
		}

		// currentAgent tracks the active agent while walking this session's
		// messages. Initialized from the session metadata agent_name so that
		// records before the first hook-matched toolUse use the session's own
		// agent. Once a hook preToolUse supplies a different agent (e.g. after
		// delegation), currentAgent is updated and subsequent records inherit
		// the new value. Falls back to the first non-empty agent in hook logs
		// when the session metadata has no agent_name (e.g. sub-agent sessions).
		currentAgent := meta.SessionState.AgentName
		if currentAgent == "" {
			for _, h := range sessionHooks {
				if h.Agent != "" {
					currentAgent = h.Agent
					break
				}
			}
		}

		// Aggregate token/credit totals from per-turn metadata.
		var inputTok, outputTok int
		var credits float64
		for _, utm := range meta.SessionState.ConversationMetadata.UserTurnMetadatas {
			inputTok += utm.InputTokenCount
			outputTok += utm.OutputTokenCount
			for _, mu := range utm.MeteringUsage {
				credits += mu.Value
			}
		}
		if inputTok > 0 || outputTok > 0 || credits > 0 {
			out = append(out, MergedRecord{
				SessionID:         meta.SessionID,
				Kind:              "sessionMeta",
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

		// Count toolUse positions for position-based matching.
		toolUseIdx := 0
		// Track the start index of this session's records for postToolUse pass.
		sessionStart := len(out)

		for _, msg := range s.Messages {
			switch msg.Kind {
			case "Prompt":
				var pd PromptData
				if err := json.Unmarshal(msg.Data, &pd); err != nil {
					continue
				}
				promptTs := time.Unix(pd.Meta.Timestamp, 0).UTC()
				var promptText string
				for _, ci := range pd.Content {
					if ci.Kind == "text" {
						var t string
						if err := json.Unmarshal(ci.Data, &t); err == nil {
							promptText += t
						}
					}
				}
				out = append(out, MergedRecord{
					SessionID:  meta.SessionID,
					Kind:       "prompt",
					PromptText: promptText,
					PromptTs:   promptTs,
					Agent:      currentAgent,
					Title:      meta.Title,
					Cwd:        meta.Cwd,
					CreatedAt:  createdAt,
					UpdatedAt:  updatedAt,
				})

			case "AssistantMessage":
				var ad AssistantData
				if err := json.Unmarshal(msg.Data, &ad); err != nil {
					continue
				}
				for _, ci := range ad.Content {
					switch ci.Kind {
					case "text":
						var t string
						if err := json.Unmarshal(ci.Data, &t); err == nil {
							out = append(out, MergedRecord{
								SessionID:     meta.SessionID,
								Kind:          "assistantText",
								AssistantText: t,
								Agent:         currentAgent,
								Title:         meta.Title,
								Cwd:           meta.Cwd,
								CreatedAt:     createdAt,
								UpdatedAt:     updatedAt,
							})
						}
					case "toolUse":
						var tu ToolUseData
						if err := json.Unmarshal(ci.Data, &tu); err != nil {
							continue
						}
						// Position-based hook matching.
						var preTs time.Time
						if toolUseIdx < len(preHooks) {
							preTs = preHooks[toolUseIdx].Ts
							if preHooks[toolUseIdx].Agent != "" {
								currentAgent = preHooks[toolUseIdx].Agent
							}
						}
						out = append(out, MergedRecord{
							SessionID: meta.SessionID,
							Kind:      "toolUse",
							ToolUseID: tu.ToolUseID,
							ToolName:  tu.Name,
							ToolInput: tu.Input,
							PreToolTs: preTs,
							Agent:     currentAgent,
							Title:     meta.Title,
							Cwd:       meta.Cwd,
							CreatedAt: createdAt,
							UpdatedAt: updatedAt,
						})
						toolUseIdx++
					}
				}

			case "ToolResults":
				var trs struct {
					Content []ContentItem `json:"content"`
				}
				if err := json.Unmarshal(msg.Data, &trs); err != nil {
					continue
				}
				for _, ci := range trs.Content {
					if ci.Kind != "toolResult" {
						continue
					}
					var tr ToolResultData
					if err := json.Unmarshal(ci.Data, &tr); err != nil {
						continue
					}
					var errorDetail string
					if tr.Status == "error" && len(tr.Content) > 0 {
						var d string
						if err := json.Unmarshal(tr.Content[0].Data, &d); err == nil {
							errorDetail = d
						}
					}
					out = append(out, MergedRecord{
						SessionID:   meta.SessionID,
						Kind:        "toolResult",
						ToolUseID:   tr.ToolUseID,
						ToolStatus:  tr.Status,
						ErrorDetail: errorDetail,
						Agent:       currentAgent,
						Title:       meta.Title,
						Cwd:         meta.Cwd,
						CreatedAt:   createdAt,
						UpdatedAt:   updatedAt,
					})
				}
			}
		}

		// Second pass: attach postToolUse data to toolResult records by position.
		if len(postHooks) > 0 {
			postIdx := 0
			for i := sessionStart; i < len(out); i++ {
				if out[i].Kind != "toolResult" {
					continue
				}
				if postIdx < len(postHooks) {
					out[i].PostToolTs = postHooks[postIdx].Ts
					if postHooks[postIdx].Agent != "" {
						out[i].Agent = postHooks[postIdx].Agent
					}
					out[i].ShellExitStatus = postHooks[postIdx].ShellExitStatus
					postIdx++
				}
			}
		}
	}

	return out
}
