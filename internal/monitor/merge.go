package monitor

import (
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
	type tsPrompt struct {
		ts  int64 // unix nanos for stable sort
		seq int   // tiebreak on equal ts
		p   string
	}
	var prompts []tsPrompt

	for _, sd := range src {
		refs = append(refs, AgentRef{Agent: sd.Agent, AgentKey: sd.AgentKey})
		merged.ToolCalls += sd.ToolCalls
		merged.Prompts += sd.Prompts
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

		// Pair each prompt with its timestamp by walking this agent's timeline
		// in chronological order and this agent's PromptHistory in reversed
		// order (PromptHistory is newest-first; Timeline is oldest-first).
		ph := sd.PromptHistory
		if len(ph) > 0 {
			chronological := make([]string, len(ph))
			for i, s := range ph {
				chronological[len(ph)-1-i] = s
			}
			i := 0
			for _, ev := range sd.Timeline {
				if ev.Event != apmconfig.EventUserPromptSubmit {
					continue
				}
				if i >= len(chronological) {
					break
				}
				prompts = append(prompts, tsPrompt{ts: ev.Ts.UnixNano(), seq: len(prompts), p: chronological[i]})
				i++
			}
			// Any unmapped prompts: append with zero ts so they sort oldest.
			for ; i < len(chronological); i++ {
				prompts = append(prompts, tsPrompt{ts: 0, seq: len(prompts), p: chronological[i]})
			}
		}
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

	// Sort prompts newest-first by paired ts (tiebreak: later seq first to
	// preserve relative input order for equal ts).
	slices.SortStableFunc(prompts, func(a, b tsPrompt) int {
		if a.ts != b.ts {
			if a.ts > b.ts {
				return -1
			}
			return 1
		}
		if a.seq > b.seq {
			return -1
		}
		return 1
	})
	merged.PromptHistory = make([]string, len(prompts))
	for i, p := range prompts {
		merged.PromptHistory[i] = p.p
	}
	if merged.PromptHistory == nil {
		merged.PromptHistory = []string{}
	}

	merged.ToolSummary = mergeToolSummary(src)
	merged.AssistantResponse = mergeAssistantResponse(src)

	return merged, refs
}

// mergeToolSummary groups ToolSummary entries by Tool across all agents,
// sums counts, recomputes SuccessRate and weighted AvgDuration, sorts by CallCount desc.
func mergeToolSummary(src []SessionDetail) []SessionToolSummary {
	type agg struct {
		callCount    int
		errorCount   int
		durWeightSum time.Duration // sum(AvgDuration * successCount)
		successCount int
	}
	m := map[string]*agg{}
	for _, sd := range src {
		for _, ts := range sd.ToolSummary {
			a := m[ts.Tool]
			if a == nil {
				a = &agg{}
				m[ts.Tool] = a
			}
			a.callCount += ts.CallCount
			a.errorCount += ts.ErrorCount
			sc := ts.CallCount - ts.ErrorCount
			if sc > 0 && ts.AvgDuration > 0 {
				a.durWeightSum += time.Duration(ts.AvgDuration) * time.Duration(sc)
				a.successCount += sc
			}
		}
	}
	if len(m) == 0 {
		return nil
	}
	out := make([]SessionToolSummary, 0, len(m))
	for tool, a := range m {
		var rate float64
		if a.callCount > 0 {
			rate = float64(a.callCount-a.errorCount) / float64(a.callCount)
		}
		var avg JSONDuration
		if a.successCount > 0 {
			avg = JSONDuration(a.durWeightSum / time.Duration(a.successCount))
		}
		out = append(out, SessionToolSummary{
			Tool: tool, CallCount: a.callCount, ErrorCount: a.errorCount,
			SuccessRate: rate, AvgDuration: avg,
		})
	}
	slices.SortFunc(out, func(a, b SessionToolSummary) int {
		if a.CallCount != b.CallCount {
			if a.CallCount > b.CallCount {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Tool, b.Tool)
	})
	return out
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
		if len(r) > maxAssistantResponseLength {
			r = r[:maxAssistantResponseLength]
		}
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
	if len(out) > maxAssistantResponseLength {
		out = out[:maxAssistantResponseLength]
	}
	return out
}


