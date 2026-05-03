package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

const ideHookAgent = "kiro-ide"

// IDEHookInputRecord is one raw IDE runCommand hook capture from hook-dump.
type IDEHookInputRecord struct {
	Ts             time.Time
	Event          string
	Agent          string
	Cwd            string
	Stdin          string
	StdinBytes     int
	StdinReadTimed bool
	Env            map[string]string
	EnvKeys        []string
}

type ideHookInputJSON struct {
	Ts             string            `json:"ts"`
	Event          string            `json:"event,omitempty"`
	Agent          string            `json:"agent,omitempty"`
	Cwd            string            `json:"cwd,omitempty"`
	Stdin          string            `json:"stdin"`
	StdinBytes     int               `json:"stdin_bytes"`
	StdinReadTimed bool              `json:"stdin_read_timed_out,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	EnvKeys        []string          `json:"env_keys,omitempty"`
}

// LoadIDEHookInputs reads hook-dump's raw IDE hook capture file.
func LoadIDEHookInputs(ctx context.Context, hookLogsDir string) ([]IDEHookInputRecord, error) {
	if hookLogsDir == "" {
		return nil, nil
	}
	path := filepath.Join(hookLogsDir, "hook-input.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var records []IDEHookInputRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 20*1024*1024)
	var parseFailed int
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw ideHookInputJSON
		if err := json.Unmarshal(line, &raw); err != nil {
			parseFailed++
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, raw.Ts)
		if err != nil || ts.IsZero() {
			continue
		}
		records = append(records, IDEHookInputRecord{
			Ts:             ts,
			Event:          raw.Event,
			Agent:          raw.Agent,
			Cwd:            raw.Cwd,
			Stdin:          raw.Stdin,
			StdinBytes:     raw.StdinBytes,
			StdinReadTimed: raw.StdinReadTimed,
			Env:            raw.Env,
			EnvKeys:        raw.EnvKeys,
		})
	}
	if parseFailed > 0 {
		slog.Warn("skipped malformed IDE hook input lines", "path", path, "count", parseFailed)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// AppendIDEHookMergedRecords folds raw IDE hook captures into the same record
// stream used by CLI and IDE execution logs. Existing IDE execution records are
// enriched when possible; otherwise synthetic tool records preserve hook data.
func AppendIDEHookMergedRecords(records []MergedRecord, sessions []IDEParsedSession, hooks []IDEHookInputRecord) []MergedRecord {
	if len(sessions) == 0 || len(hooks) == 0 {
		return records
	}
	sorted := slices.Clone(hooks)
	slices.SortStableFunc(sorted, func(a, b IDEHookInputRecord) int { return a.Ts.Compare(b.Ts) })

	windows := ideSessionWindows(sessions, records)
	pendingPre := map[string][]IDEHookInputRecord{}
	promptsBySession := map[string][]ideHookPrompt{}
	var synthetic []MergedRecord
	for _, h := range sorted {
		s := matchIDEHookSession(sessions, windows, h)
		if s == nil {
			continue
		}
		sid := s.SessionID
		switch h.Event {
		case apmconfig.EventUserPromptSubmit:
			prompt := hookUserPrompt(h)
			if strings.TrimSpace(prompt) == "" {
				continue
			}
			promptsBySession[sid] = append(promptsBySession[sid], ideHookPrompt{text: prompt, ts: h.Ts})
			synthetic = append(synthetic, ideHookBaseRecord(*s, h, RecordKindPrompt, ""))
			synthetic[len(synthetic)-1].PromptText = prompt
			synthetic[len(synthetic)-1].PromptTs = h.Ts

		case apmconfig.EventPreToolUse:
			pendingPre[sid] = append(pendingPre[sid], h)

		case apmconfig.EventPostToolUse:
			if pendingSID := bestPendingPreSession(pendingPre, h); pendingSID != "" {
				if pendingSession := findIDESessionByID(sessions, pendingSID); pendingSession != nil {
					s = pendingSession
					sid = pendingSID
				}
			}
			payload := parseIDEHookToolPayload(h)
			if payload.ToolName == "" {
				payload.ToolName = "tool"
			}
			payload.ToolName = normalizeIDEToolName(payload.ToolName)
			if enrichExistingIDEResult(records, sid, payload, h.Ts) || enrichAnyExistingIDEResult(records, payload, h.Ts) {
				pendingPre[sid] = popPendingPre(pendingPre[sid])
				continue
			}
			preTs := h.Ts
			if len(pendingPre[sid]) > 0 {
				preTs = pendingPre[sid][0].Ts
				pendingPre[sid] = pendingPre[sid][1:]
			}
			synthetic = append(synthetic, ideHookToolPair(*s, h, payload, preTs)...)

		case apmconfig.EventStop:
			rec := ideHookBaseRecord(*s, h, RecordKindStop, "")
			rec.PreToolTs = h.Ts
			synthetic = append(synthetic, rec)

		case "manual", "manualSmoke":
			continue

		default:
			if isEmptyLifecycleHookPayload(h) {
				continue
			}
			rec := ideHookBaseRecord(*s, h, RecordKindHookEvent, "")
			rec.EventName = h.Event
			rec.ToolInput = genericIDEHookInput(h)
			rec.ToolResult = hookUserPrompt(h)
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
			rec := ideHookBaseRecord(*s, h, RecordKindToolUse, "tool")
			rec.ToolUseID = ideHookToolUseID(h)
			rec.ToolName = "tool"
			rec.ToolInput = genericIDEHookInput(h)
			rec.PreToolTs = h.Ts
			synthetic = append(synthetic, rec)
		}
	}

	if len(promptsBySession) > 0 {
		for i := range records {
			if records[i].Kind == RecordKindSessionMeta {
				if hooks := promptsBySession[records[i].SessionID]; len(hooks) > 0 {
					s := findIDESessionByID(sessions, records[i].SessionID)
					if s == nil {
						continue
					}
					synthetic = append(synthetic, fallbackPromptRecords(*s, records[i].PromptTexts, hooks)...)
					records[i].PromptTexts = nil
				}
			}
		}
	}

	return append(records, synthetic...)
}

func isEmptyLifecycleHookPayload(h IDEHookInputRecord) bool {
	if h.Stdin != "" || hookUserPrompt(h) != "" {
		return false
	}
	switch h.Event {
	case apmconfig.EventFileCreated,
		apmconfig.EventFileEdited,
		apmconfig.EventFileDeleted,
		apmconfig.EventPreTaskExecution,
		apmconfig.EventPostTaskExecution:
		return true
	default:
		return false
	}
}

type ideHookPayload struct {
	ToolName   string
	ToolArgs   json.RawMessage
	ToolResult string
	Success    *bool
}

type ideHookPrompt struct {
	text string
	ts   time.Time
}

func parseIDEHookToolPayload(h IDEHookInputRecord) ideHookPayload {
	var payload struct {
		ToolName    string          `json:"toolName"`
		ToolArgs    json.RawMessage `json:"toolArgs"`
		ToolResult  string          `json:"toolResult"`
		ToolSuccess *bool           `json:"toolSuccess"`
	}
	_ = json.Unmarshal([]byte(hookUserPrompt(h)), &payload)
	return ideHookPayload{
		ToolName:   payload.ToolName,
		ToolArgs:   normalizedRawJSON(payload.ToolArgs),
		ToolResult: payload.ToolResult,
		Success:    payload.ToolSuccess,
	}
}

func ideHookToolPair(s IDEParsedSession, h IDEHookInputRecord, payload ideHookPayload, preTs time.Time) []MergedRecord {
	id := ideHookToolUseID(h)
	payload.ToolName = normalizeIDEToolName(payload.ToolName)
	toolInput := normalizedRawJSON(payload.ToolArgs)
	use := ideHookBaseRecord(s, h, RecordKindToolUse, payload.ToolName)
	use.ToolUseID = id
	use.ToolName = payload.ToolName
	use.ToolInput = toolInput
	use.PreToolTs = preTs

	status := ToolStatusSuccess
	errorDetail := ""
	if payload.Success != nil && !*payload.Success {
		status = ToolStatusError
		errorDetail = truncateUTF8(payload.ToolResult, maxErrorDetailLength)
	}
	result := ideHookBaseRecord(s, h, RecordKindToolResult, payload.ToolName)
	result.ToolUseID = id
	result.ToolName = payload.ToolName
	result.ToolStatus = status
	result.ErrorDetail = errorDetail
	result.ToolResult = payload.ToolResult
	result.PostToolTs = h.Ts
	return []MergedRecord{use, result}
}

func ideHookBaseRecord(s IDEParsedSession, h IDEHookInputRecord, kind, tool string) MergedRecord {
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

func enrichExistingIDEResult(records []MergedRecord, sid string, payload ideHookPayload, ts time.Time) bool {
	best := bestExistingIDEResult(records, sid, payload, ts)
	if best < 0 {
		return false
	}
	enrichExistingIDEResultAt(records, best, payload)
	return true
}

func enrichAnyExistingIDEResult(records []MergedRecord, payload ideHookPayload, ts time.Time) bool {
	best := bestExistingIDEResult(records, "", payload, ts)
	if best < 0 {
		return false
	}
	enrichExistingIDEResultAt(records, best, payload)
	return true
}

func bestExistingIDEResult(records []MergedRecord, sid string, payload ideHookPayload, ts time.Time) int {
	best := -1
	bestDelta := 5 * time.Second
	for i := range records {
		r := records[i]
		if sid != "" && r.SessionID != sid {
			continue
		}
		if r.Kind != RecordKindToolResult {
			continue
		}
		if normalizeIDEToolName(r.ToolName) != normalizeIDEToolName(payload.ToolName) {
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

func enrichExistingIDEResultAt(records []MergedRecord, best int, payload ideHookPayload) {
	if payload.ToolResult != "" {
		records[best].ToolResult = payload.ToolResult
	}
	if !rawJSONIsEmptyObject(payload.ToolArgs) {
		sid := records[best].SessionID
		toolUseID := records[best].ToolUseID
		for i := range records {
			if records[i].SessionID == sid && records[i].Kind == RecordKindToolUse && records[i].ToolUseID == toolUseID {
				if len(records[i].ToolInput) == 0 || rawJSONIsEmptyObject(records[i].ToolInput) {
					records[i].ToolInput = payload.ToolArgs
				}
				break
			}
		}
	}
	if payload.Success != nil && !*payload.Success {
		records[best].ToolStatus = ToolStatusError
		records[best].ErrorDetail = truncateUTF8(payload.ToolResult, maxErrorDetailLength)
	}
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

func matchIDEHookSession(sessions []IDEParsedSession, windows map[string]ideSessionWindow, h IDEHookInputRecord) *IDEParsedSession {
	var best *IDEParsedSession
	var bestWindow *IDEParsedSession
	const windowGrace = 2 * time.Minute
	for i := range sessions {
		s := &sessions[i]
		if h.Cwd != "" && s.WorkspaceDirectory != "" && h.Cwd != s.WorkspaceDirectory {
			continue
		}
		if !s.CreatedAt.IsZero() && h.Ts.Before(s.CreatedAt) {
			continue
		}
		if w, ok := windows[s.SessionID]; ok && !w.start.IsZero() {
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

func fallbackPromptRecords(s IDEParsedSession, fallbacks []string, hooks []ideHookPrompt) []MergedRecord {
	if len(fallbacks) == 0 {
		return nil
	}
	used := make([]bool, len(hooks))
	out := make([]MergedRecord, 0, len(fallbacks))
	lastHookTs := s.CreatedAt
	for idx, fallback := range fallbacks {
		matched := false
		for i, hook := range hooks {
			if used[i] {
				continue
			}
			if strings.TrimSpace(fallback) == strings.TrimSpace(hook.text) {
				used[i] = true
				matched = true
				lastHookTs = hook.ts
				break
			}
		}
		if !matched {
			ts := lastHookTs.Add(time.Duration(idx+1) * time.Nanosecond)
			rec := ideHookBaseRecord(s, IDEHookInputRecord{Ts: ts}, RecordKindPrompt, "")
			rec.PromptText = fallback
			rec.PromptTs = ts
			out = append(out, rec)
		}
	}
	return out
}

func findIDESessionByID(sessions []IDEParsedSession, sid string) *IDEParsedSession {
	for i := range sessions {
		if sessions[i].SessionID == sid {
			return &sessions[i]
		}
	}
	return nil
}

func hookUserPrompt(h IDEHookInputRecord) string {
	if h.Env == nil {
		return ""
	}
	return h.Env["USER_PROMPT"]
}

func popPendingPre(records []IDEHookInputRecord) []IDEHookInputRecord {
	if len(records) == 0 {
		return records
	}
	return records[1:]
}

func bestPendingPreSession(pending map[string][]IDEHookInputRecord, post IDEHookInputRecord) string {
	var bestSID string
	var bestDelta time.Duration
	for sid, records := range pending {
		if len(records) == 0 {
			continue
		}
		pre := records[0]
		if post.Cwd != "" && pre.Cwd != "" && post.Cwd != pre.Cwd {
			continue
		}
		if pre.Ts.After(post.Ts) {
			continue
		}
		delta := post.Ts.Sub(pre.Ts)
		if delta > 2*time.Minute {
			continue
		}
		if bestSID == "" || delta < bestDelta {
			bestSID = sid
			bestDelta = delta
		}
	}
	return bestSID
}

func ideHookToolUseID(h IDEHookInputRecord) string {
	return fmt.Sprintf("ide-hook-%d", h.Ts.UnixNano())
}

func normalizedRawJSON(raw json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	return raw
}

func rawJSONIsEmptyObject(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "{}" || trimmed == "null"
}

func genericIDEHookInput(h IDEHookInputRecord) json.RawMessage {
	payload := map[string]string{}
	if h.Stdin != "" {
		payload["stdin"] = h.Stdin
	}
	if userPrompt := hookUserPrompt(h); userPrompt != "" {
		payload["userPrompt"] = userPrompt
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

func normalizeIDEToolName(name string) string {
	switch name {
	case "readFile", "readMultipleFiles", ActionReadFiles:
		return ActionReadFiles
	case "fsWrite":
		return ActionCreate
	case "deleteFile":
		return ActionDelete
	case "executeCommand", ActionRunCommand, ToolNameShell:
		return ToolNameShell
	default:
		return name
	}
}
