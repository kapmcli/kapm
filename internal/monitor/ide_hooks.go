package monitor

import (
	"slices"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

const ideHookAgent = "kiro-ide"

// AppendIDEHookRecords folds minimal IDE hook records into IDE session and
// execution records. Hook logs provide lifecycle timestamps only; prompts,
// tool inputs, and tool results remain sourced from IDE session/execution logs.
func AppendIDEHookRecords(records []MergedRecord, sessions []IDEParsedSession, hooks []HookRecord) []MergedRecord {
	if len(sessions) == 0 || len(hooks) == 0 {
		return records
	}
	sorted := slices.Clone(hooks)
	slices.SortStableFunc(sorted, func(a, b HookRecord) int { return a.Ts.Compare(b.Ts) })

	windows := ideSessionWindows(sessions, records)
	pendingPre := map[string][]HookRecord{}
	promptState := map[string]idePromptState{}
	var synthetic []MergedRecord
	for _, h := range sorted {
		s := matchIDEHookSession(sessions, windows, h)
		if s == nil {
			continue
		}
		sid := s.SessionID
		switch h.Event {
		case apmconfig.EventUserPromptSubmit:
			prompt := nextIDEHookPrompt(s, promptState[sid])
			if prompt == "" {
				continue
			}
			state := promptState[sid]
			state.index++
			state.lastTs = h.Ts
			promptState[sid] = state

			rec := ideHookBaseRecord(*s, h, RecordKindPrompt, "")
			rec.PromptText = prompt
			rec.PromptTs = h.Ts
			synthetic = append(synthetic, rec)

		case apmconfig.EventPreToolUse:
			pendingPre[sid] = append(pendingPre[sid], h)

		case apmconfig.EventPostToolUse:
			preTs := h.Ts
			var matchedPre *HookRecord
			if len(pendingPre[sid]) > 0 {
				matchedPre = &pendingPre[sid][0]
				preTs = matchedPre.Ts
				pendingPre[sid] = pendingPre[sid][1:]
			}
			if enrichExistingIDEHookPair(records, sid, preTs, h.Ts) {
				continue
			}
			if matchedPre != nil {
				synthetic = append(synthetic, ideHookLifecycleRecord(*s, *matchedPre, apmconfig.EventPreToolUse, preTs))
			}
			synthetic = append(synthetic, ideHookLifecycleRecord(*s, h, apmconfig.EventPostToolUse, h.Ts))

		case apmconfig.EventStop:
			rec := ideHookBaseRecord(*s, h, RecordKindStop, "")
			rec.PreToolTs = h.Ts
			synthetic = append(synthetic, rec)

		case "manual", "manualSmoke":
			continue

		default:
			if h.Event == "" {
				continue
			}
			rec := ideHookBaseRecord(*s, h, RecordKindHookEvent, "")
			rec.EventName = h.Event
			rec.PreToolTs = h.Ts
			synthetic = append(synthetic, rec)
		}
	}

	for sid, pres := range pendingPre {
		s := findIDESessionByID(sessions, sid)
		if s == nil {
			continue
		}
		for _, h := range pres {
			synthetic = append(synthetic, ideHookLifecycleRecord(*s, h, apmconfig.EventPreToolUse, h.Ts))
		}
	}

	appendIDEHookPromptFallbacks(records, sessions, promptState, &synthetic)
	return append(records, synthetic...)
}

type ideSessionWindow struct {
	start time.Time
	end   time.Time
}

func ideSessionWindows(sessions []IDEParsedSession, records []MergedRecord) map[string]ideSessionWindow {
	windows := make(map[string]ideSessionWindow, len(sessions))
	for _, s := range sessions {
		windows[s.SessionID] = ideSessionWindow{start: s.CreatedAt, end: s.CreatedAt}
	}
	for _, r := range records {
		w, ok := windows[r.SessionID]
		if !ok {
			continue
		}
		for _, ts := range []time.Time{r.CreatedAt, r.UpdatedAt, r.PromptTs, r.PreToolTs, r.PostToolTs} {
			if ts.IsZero() {
				continue
			}
			if w.start.IsZero() || ts.Before(w.start) {
				w.start = ts
			}
			if w.end.IsZero() || ts.After(w.end) {
				w.end = ts
			}
		}
		windows[r.SessionID] = w
	}
	return windows
}

func matchIDEHookSession(sessions []IDEParsedSession, windows map[string]ideSessionWindow, h HookRecord) *IDEParsedSession {
	var best *IDEParsedSession
	var bestWindow *IDEParsedSession
	const windowGrace = 2 * time.Minute
	for i := range sessions {
		s := &sessions[i]
		if h.Cwd != "" && s.WorkspaceDirectory != "" && h.Cwd != s.WorkspaceDirectory {
			continue
		}
		w, hasWindow := windows[s.SessionID]
		start := s.CreatedAt
		if hasWindow && !w.start.IsZero() && (start.IsZero() || w.start.Before(start)) {
			start = w.start
		}
		if !start.IsZero() && h.Ts.Before(start) {
			continue
		}
		if hasWindow && !w.start.IsZero() {
			end := w.end
			if end.IsZero() {
				end = w.start
			}
			if !h.Ts.Before(w.start) && !h.Ts.After(end.Add(windowGrace)) {
				if bestWindow == nil || w.start.After(windows[bestWindow.SessionID].start) {
					bestWindow = s
				}
			}
		}
		if best == nil || s.CreatedAt.After(best.CreatedAt) {
			best = s
		}
	}
	if bestWindow != nil {
		return bestWindow
	}
	return best
}

type idePromptState struct {
	index  int
	lastTs time.Time
}

func nextIDEHookPrompt(s *IDEParsedSession, state idePromptState) string {
	if state.index >= len(s.PromptTexts) {
		return ""
	}
	return s.PromptTexts[state.index]
}

func appendIDEHookPromptFallbacks(records []MergedRecord, sessions []IDEParsedSession, promptState map[string]idePromptState, synthetic *[]MergedRecord) {
	for i := range records {
		if records[i].Kind != RecordKindSessionMeta {
			continue
		}
		state, ok := promptState[records[i].SessionID]
		if !ok || state.index == 0 {
			continue
		}
		s := findIDESessionByID(sessions, records[i].SessionID)
		if s == nil {
			continue
		}
		for idx := state.index; idx < len(records[i].PromptTexts); idx++ {
			ts := state.lastTs.Add(time.Duration(idx-state.index+1) * time.Nanosecond)
			rec := ideHookBaseRecord(*s, HookRecord{Ts: ts}, RecordKindPrompt, "")
			rec.PromptText = records[i].PromptTexts[idx]
			rec.PromptTs = ts
			*synthetic = append(*synthetic, rec)
		}
		records[i].PromptTexts = nil
	}
}

func ideHookLifecycleRecord(s IDEParsedSession, h HookRecord, event string, ts time.Time) MergedRecord {
	rec := ideHookBaseRecord(s, h, RecordKindHookEvent, "")
	rec.EventName = event
	rec.PreToolTs = ts
	return rec
}

func ideHookBaseRecord(s IDEParsedSession, h HookRecord, kind, tool string) MergedRecord {
	return MergedRecord{
		SessionID: s.SessionID,
		Kind:      kind,
		Agent:     ideHookAgent,
		ToolName:  tool,
		Title:     s.Title,
		Cwd:       s.WorkspaceDirectory,
		CreatedAt: s.CreatedAt,
		UpdatedAt: h.Ts,
	}
}

func enrichExistingIDEHookPair(records []MergedRecord, sid string, preTs, postTs time.Time) bool {
	best := bestExistingIDEResult(records, sid, postTs)
	if best < 0 {
		return false
	}
	records[best].PostToolTs = postTs
	toolUseID := records[best].ToolUseID
	for i := range records {
		if records[i].SessionID == sid && records[i].Kind == RecordKindToolUse && records[i].ToolUseID == toolUseID {
			records[i].PreToolTs = preTs
			break
		}
	}
	return true
}

func bestExistingIDEResult(records []MergedRecord, sid string, ts time.Time) int {
	best := -1
	bestDelta := 5 * time.Second
	for i := range records {
		r := records[i]
		if r.SessionID != sid || r.Kind != RecordKindToolResult {
			continue
		}
		if r.PostToolTs.IsZero() {
			continue
		}
		delta := r.PostToolTs.Sub(ts)
		if delta < 0 {
			delta = -delta
		}
		if delta < bestDelta {
			best = i
			bestDelta = delta
		}
	}
	return best
}

func findIDESessionByID(sessions []IDEParsedSession, sid string) *IDEParsedSession {
	for i := range sessions {
		if sessions[i].SessionID == sid {
			return &sessions[i]
		}
	}
	return nil
}
