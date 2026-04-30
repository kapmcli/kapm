package monitor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"github.com/kapmcli/kapm/internal/testutil"
)

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func makeSession(id string, msgs []SessionMessage) ParsedSession {
	return ParsedSession{
		Meta: SessionMeta{
			SessionID: id,
			Title:     "test",
			Cwd:       "/tmp",
			CreatedAt: rfc3339Time(time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)),
			UpdatedAt: rfc3339Time(time.Date(2026, 4, 27, 10, 1, 0, 0, time.UTC)),
		},
		Messages: msgs,
	}
}

func promptMsg(text string, ts int64) SessionMessage {
	pd := PromptData{
		MessageID: "msg-1",
		Content:   []ContentItem{{Kind: ContentKindText, Data: mustJSON(text)}},
		Meta:      PromptMeta{Timestamp: ts},
	}
	return SessionMessage{Kind: MessageKindPrompt, Data: mustJSON(pd)}
}

func toolUseMsg(id, name string, input any) SessionMessage {
	tu := ToolUseData{ToolUseID: id, Name: name, Input: mustJSON(input)}
	ad := AssistantData{
		MessageID: "msg-2",
		Content:   []ContentItem{{Kind: ContentKindToolUse, Data: mustJSON(tu)}},
	}
	return SessionMessage{Kind: MessageKindAssistantMessage, Data: mustJSON(ad)}
}

func toolResultMsg(id, status string) SessionMessage {
	tr := ToolResultData{
		ToolUseID: id,
		Status:    status,
		Content:   []ContentItem{{Kind: ContentKindText, Data: mustJSON("ok")}},
	}
	trs := struct {
		Content []ContentItem `json:"content"`
	}{Content: []ContentItem{{Kind: ContentKindToolResult, Data: mustJSON(tr)}}}
	return SessionMessage{Kind: MessageKindToolResults, Data: mustJSON(trs)}
}

func toolResultErrMsg(id, errText string) SessionMessage {
	tr := ToolResultData{
		ToolUseID: id,
		Status:    ToolStatusError,
		Content:   []ContentItem{{Kind: ContentKindText, Data: mustJSON(errText)}},
	}
	trs := struct {
		Content []ContentItem `json:"content"`
	}{Content: []ContentItem{{Kind: ContentKindToolResult, Data: mustJSON(tr)}}}
	return SessionMessage{Kind: MessageKindToolResults, Data: mustJSON(trs)}
}

// TestMergeSessions_SessionsOnly verifies sessions-only mode: no hook data,
// all hook fields are zero values.
func TestMergeSessions_SessionsOnly(t *testing.T) {
	session := makeSession("sess-1", []SessionMessage{
		promptMsg("hello", 1745744400),
		toolUseMsg("tu-1", "read", map[string]string{"path": "/tmp/f"}),
		toolResultMsg("tu-1", "success"),
	})

	recs := MergeSessions([]ParsedSession{session}, nil)

	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}

	prompt := recs[0]
	if prompt.Kind != RecordKindPrompt {
		t.Errorf("recs[0].Kind = %q, want prompt", prompt.Kind)
	}
	if prompt.PromptText != "hello" {
		t.Errorf("PromptText = %q, want hello", prompt.PromptText)
	}
	if !prompt.PreToolTs.IsZero() || !prompt.PostToolTs.IsZero() || prompt.Agent != "" {
		t.Error("hook fields should be zero for sessions-only")
	}

	tu := recs[1]
	if tu.Kind != RecordKindToolUse {
		t.Errorf("recs[1].Kind = %q, want toolUse", tu.Kind)
	}
	if tu.ToolName != "read" {
		t.Errorf("ToolName = %q, want read", tu.ToolName)
	}
	if !tu.PreToolTs.IsZero() || tu.Agent != "" {
		t.Error("hook fields should be zero for sessions-only toolUse")
	}

	tr := recs[2]
	if tr.Kind != RecordKindToolResult {
		t.Errorf("recs[2].Kind = %q, want toolResult", tr.Kind)
	}
	if tr.ToolStatus != ToolStatusSuccess {
		t.Errorf("ToolStatus = %q, want success", tr.ToolStatus)
	}
	if !tr.PostToolTs.IsZero() || tr.ShellExitStatus != "" {
		t.Error("hook fields should be zero for sessions-only toolResult")
	}
}

// TestMergeSessions_WithHook verifies position-based matching: hook ts/agent/
// shell_exit_status are reflected in MergedRecord.
func TestMergeSessions_WithHook(t *testing.T) {
	session := makeSession("sess-2", []SessionMessage{
		toolUseMsg("tu-1", "bash", map[string]string{"cmd": "ls"}),
		toolResultMsg("tu-1", "success"),
	})

	preTs := time.Date(2026, 4, 27, 10, 19, 42, 0, time.UTC)
	postTs := time.Date(2026, 4, 27, 10, 19, 43, 0, time.UTC)
	hooks := []HookRecord{
		{Ts: preTs, Session: "sess-2", Event: "preToolUse", Agent: "orchestrator", Tool: "bash"},
		{Ts: postTs, Session: "sess-2", Event: "postToolUse", Agent: "orchestrator", Tool: "bash", ShellExitStatus: "0"},
	}

	recs := MergeSessions([]ParsedSession{session}, hooks)

	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}

	tu := recs[0]
	if tu.Kind != RecordKindToolUse {
		t.Errorf("recs[0].Kind = %q, want toolUse", tu.Kind)
	}
	if !tu.PreToolTs.Equal(preTs) {
		t.Errorf("PreToolTs = %v, want %v", tu.PreToolTs, preTs)
	}
	if tu.Agent != "orchestrator" {
		t.Errorf("Agent = %q, want orchestrator", tu.Agent)
	}

	tr := recs[1]
	if tr.Kind != RecordKindToolResult {
		t.Errorf("recs[1].Kind = %q, want toolResult", tr.Kind)
	}
	if !tr.PostToolTs.Equal(postTs) {
		t.Errorf("PostToolTs = %v, want %v", tr.PostToolTs, postTs)
	}
	if tr.ShellExitStatus != "0" {
		t.Errorf("ShellExitStatus = %q, want 0", tr.ShellExitStatus)
	}
}

// TestMergeSessions_FewerHooksThanToolUse verifies position-based matching with fewer hooks than toolUse records.
func TestMergeSessions_FewerHooksThanToolUse(t *testing.T) {
	// Position-based matching: 2 toolUse, 1 hook pair → hook[0] matches toolUse[0], toolUse[1] has no hook.
	session := makeSession("sess-3", []SessionMessage{
		toolUseMsg("tu-1", "read", map[string]string{"path": "/a"}),
		toolResultMsg("tu-1", "success"),
		toolUseMsg("tu-2", "bash", map[string]string{"cmd": "ls"}),
		toolResultMsg("tu-2", "success"),
	})

	preTs := time.Date(2026, 4, 27, 11, 0, 0, 0, time.UTC)
	postTs := time.Date(2026, 4, 27, 11, 0, 1, 0, time.UTC)
	hooks := []HookRecord{
		{Ts: preTs, Session: "sess-3", Event: "preToolUse", Agent: "agent", Tool: "read"},
		{Ts: postTs, Session: "sess-3", Event: "postToolUse", Agent: "agent", Tool: "read", ShellExitStatus: "1"},
	}

	recs := MergeSessions([]ParsedSession{session}, hooks)

	if len(recs) != 4 {
		t.Fatalf("expected 4 records, got %d", len(recs))
	}

	tu1 := recs[0]
	if tu1.Kind != RecordKindToolUse {
		t.Errorf("recs[0].Kind = %q, want toolUse", tu1.Kind)
	}
	if !tu1.PreToolTs.Equal(preTs) {
		t.Errorf("recs[0].PreToolTs = %v, want %v (first toolUse gets hook[0])", tu1.PreToolTs, preTs)
	}

	tu2 := recs[2]
	if tu2.Kind != RecordKindToolUse {
		t.Errorf("recs[2].Kind = %q, want toolUse", tu2.Kind)
	}
	if !tu2.PreToolTs.IsZero() {
		t.Errorf("recs[2].PreToolTs should be zero (no hook for second toolUse), got %v", tu2.PreToolTs)
	}

	tr2 := recs[3]
	if tr2.Kind != RecordKindToolResult {
		t.Errorf("recs[3].Kind = %q, want toolResult", tr2.Kind)
	}
	if !tr2.PostToolTs.IsZero() {
		t.Errorf("recs[3].PostToolTs should be zero, got %v", tr2.PostToolTs)
	}
}

func TestMergeSessions_EmptySessions(t *testing.T) {
	recs := MergeSessions([]ParsedSession{}, nil)
	if len(recs) != 0 {
		t.Fatalf("expected 0 records, got %d", len(recs))
	}
}

func TestMergeSessions_NoMessages(t *testing.T) {
	session := makeSession("sess-empty", []SessionMessage{})
	recs := MergeSessions([]ParsedSession{session}, nil)
	if len(recs) != 0 {
		t.Fatalf("expected 0 records, got %d", len(recs))
	}
}

func TestMergeSessions_OrphanHooks(t *testing.T) {
	session := makeSession("sess-real", []SessionMessage{
		toolUseMsg("tu-1", "read", map[string]string{"path": "/a"}),
		toolResultMsg("tu-1", "success"),
	})
	hooks := []HookRecord{
		{Ts: time.Now(), Session: "sess-nonexistent", Event: "preToolUse", Agent: "agent", Tool: "read"},
	}
	recs := MergeSessions([]ParsedSession{session}, hooks)
	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}
	if !recs[0].PreToolTs.IsZero() {
		t.Errorf("orphan hook should not match: PreToolTs = %v", recs[0].PreToolTs)
	}
}

// TestMergeSessions_ErrorDetail verifies ErrorDetail extraction from toolResult.
// TestMergeSessions_SessionMeta verifies that MergeSessions emits a
// "sessionMeta" record with correct token/credit totals when UserTurnMetadatas
// are present.
func TestMergeSessions_SessionMeta(t *testing.T) {
	turns := []UserTurnMetadata{
		{InputTokenCount: 100, OutputTokenCount: 200, MeteringUsage: []MeteringEntry{{Value: 0.5, Unit: "credits"}}},
		{InputTokenCount: 300, OutputTokenCount: 400, MeteringUsage: []MeteringEntry{{Value: 1.5, Unit: "credits"}}},
	}
	session := ParsedSession{
		Meta: SessionMeta{
			SessionID: "sess-tok",
			Title:     "token test",
			Cwd:       "/tmp",
			CreatedAt: rfc3339Time(time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)),
			UpdatedAt: rfc3339Time(time.Date(2026, 4, 28, 10, 1, 0, 0, time.UTC)),
			SessionState: SessionState{
				AgentName:            "coder",
				ConversationMetadata: ConversationMetadata{UserTurnMetadatas: turns},
			},
		},
		Messages: []SessionMessage{promptMsg("hello", 1745744400)},
	}

	recs := MergeSessions([]ParsedSession{session}, nil)

	var meta *MergedRecord
	for i := range recs {
		if recs[i].Kind == RecordKindSessionMeta {
			meta = &recs[i]
			break
		}
	}
	if meta == nil {
		t.Fatal("expected a sessionMeta record, got none")
	}
	if meta.TotalInputTokens != 400 {
		t.Errorf("TotalInputTokens: want 400, got %d", meta.TotalInputTokens)
	}
	if meta.TotalOutputTokens != 600 {
		t.Errorf("TotalOutputTokens: want 600, got %d", meta.TotalOutputTokens)
	}
	if meta.TotalCredits != 2.0 {
		t.Errorf("TotalCredits: want 2.0, got %f", meta.TotalCredits)
	}
	if meta.Agent != "coder" {
		t.Errorf("Agent: want coder, got %q", meta.Agent)
	}
}

// TestMergeSessions_SessionMeta_Zero verifies that no "sessionMeta" record is
// emitted when UserTurnMetadatas is empty (all token/credit fields are zero).
func TestMergeSessions_SessionMeta_Zero(t *testing.T) {
	session := makeSession("sess-zero", []SessionMessage{promptMsg("hi", 1745744400)})
	recs := MergeSessions([]ParsedSession{session}, nil)
	for _, r := range recs {
		if r.Kind == RecordKindSessionMeta {
			t.Errorf("unexpected sessionMeta record for session with no UserTurnMetadatas")
		}
	}
}

// TestMergeSessions_SessionMeta_SingleTurn verifies that a session with exactly
// one UserTurnMetadata emits a correct sessionMeta record.
func TestMergeSessions_SessionMeta_SingleTurn(t *testing.T) {
	turns := []UserTurnMetadata{
		{InputTokenCount: 50, OutputTokenCount: 75, MeteringUsage: []MeteringEntry{{Value: 0.25, Unit: "credits"}}},
	}
	session := ParsedSession{
		Meta: SessionMeta{
			SessionID: "sess-single",
			Title:     "single turn",
			Cwd:       "/tmp",
			CreatedAt: rfc3339Time(time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)),
			UpdatedAt: rfc3339Time(time.Date(2026, 4, 28, 10, 1, 0, 0, time.UTC)),
			SessionState: SessionState{
				AgentName:            "lead",
				ConversationMetadata: ConversationMetadata{UserTurnMetadatas: turns},
			},
		},
		Messages: []SessionMessage{promptMsg("hi", 1745744400)},
	}

	recs := MergeSessions([]ParsedSession{session}, nil)

	var meta *MergedRecord
	for i := range recs {
		if recs[i].Kind == RecordKindSessionMeta {
			meta = &recs[i]
			break
		}
	}
	if meta == nil {
		t.Fatal("expected a sessionMeta record, got none")
	}
	if meta.TotalInputTokens != 50 {
		t.Errorf("TotalInputTokens: want 50, got %d", meta.TotalInputTokens)
	}
	if meta.TotalOutputTokens != 75 {
		t.Errorf("TotalOutputTokens: want 75, got %d", meta.TotalOutputTokens)
	}
	if meta.TotalCredits != 0.25 {
		t.Errorf("TotalCredits: want 0.25, got %f", meta.TotalCredits)
	}
}

// TestMergeSessions_SessionMeta_ZeroCreditsWithTokens verifies that a session
// with token counts but empty MeteringUsage slices emits a sessionMeta record
// with TotalCredits=0 but non-zero token counts.
func TestMergeSessions_SessionMeta_ZeroCreditsWithTokens(t *testing.T) {
	turns := []UserTurnMetadata{
		{InputTokenCount: 120, OutputTokenCount: 180, MeteringUsage: []MeteringEntry{}},
		{InputTokenCount: 80, OutputTokenCount: 60, MeteringUsage: nil},
	}
	session := ParsedSession{
		Meta: SessionMeta{
			SessionID: "sess-nocredits",
			Title:     "no credits",
			Cwd:       "/tmp",
			CreatedAt: rfc3339Time(time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)),
			UpdatedAt: rfc3339Time(time.Date(2026, 4, 28, 10, 1, 0, 0, time.UTC)),
			SessionState: SessionState{
				AgentName:            "coder",
				ConversationMetadata: ConversationMetadata{UserTurnMetadatas: turns},
			},
		},
		Messages: []SessionMessage{promptMsg("hi", 1745744400)},
	}

	recs := MergeSessions([]ParsedSession{session}, nil)

	var meta *MergedRecord
	for i := range recs {
		if recs[i].Kind == RecordKindSessionMeta {
			meta = &recs[i]
			break
		}
	}
	if meta == nil {
		t.Fatal("expected a sessionMeta record, got none")
	}
	if meta.TotalInputTokens != 200 {
		t.Errorf("TotalInputTokens: want 200, got %d", meta.TotalInputTokens)
	}
	if meta.TotalOutputTokens != 240 {
		t.Errorf("TotalOutputTokens: want 240, got %d", meta.TotalOutputTokens)
	}
	if meta.TotalCredits != 0.0 {
		t.Errorf("TotalCredits: want 0.0, got %f", meta.TotalCredits)
	}
}

func TestMergeSessions_ErrorDetail(t *testing.T) {
	session := makeSession("sess-4", []SessionMessage{
		toolUseMsg("tu-1", "bash", map[string]string{"cmd": "fail"}),
		toolResultErrMsg("tu-1", "command not found"),
	})

	recs := MergeSessions([]ParsedSession{session}, nil)

	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}
	tr := recs[1]
	if tr.ToolStatus != ToolStatusError {
		t.Errorf("ToolStatus = %q, want error", tr.ToolStatus)
	}
	if tr.ErrorDetail != "command not found" {
		t.Errorf("ErrorDetail = %q, want 'command not found'", tr.ErrorDetail)
	}
}

// TestMergeSessions_AgentSpawnHook verifies that agentSpawn hook records
// produce MergedRecords with Kind=RecordKindAgentSpawn.
func TestMergeSessions_AgentSpawnHook(t *testing.T) {
	session := makeSession("sess-spawn", []SessionMessage{
		promptMsg("hello", 1745744400),
	})
	spawnTs := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	hooks := []HookRecord{
		{Ts: spawnTs, Session: "sess-spawn", Event: apmconfig.EventAgentSpawn, Agent: "lead"},
	}

	recs := MergeSessions([]ParsedSession{session}, hooks)

	var spawn *MergedRecord
	for i := range recs {
		if recs[i].Kind == RecordKindAgentSpawn {
			spawn = &recs[i]
			break
		}
	}
	if spawn == nil {
		t.Fatal("expected an agentSpawn record, got none")
	}
	if spawn.SessionID != "sess-spawn" {
		t.Errorf("SessionID = %q, want sess-spawn", spawn.SessionID)
	}
	if spawn.Agent != "lead" {
		t.Errorf("Agent = %q, want lead", spawn.Agent)
	}
	if !spawn.PreToolTs.Equal(spawnTs) {
		t.Errorf("PreToolTs = %v, want %v", spawn.PreToolTs, spawnTs)
	}
}

// TestMergeSessions_StopHook verifies that stop hook records produce
// MergedRecords with Kind=RecordKindStop.
func TestMergeSessions_StopHook(t *testing.T) {
	session := makeSession("sess-stop", []SessionMessage{
		promptMsg("hello", 1745744400),
	})
	stopTs := time.Date(2026, 4, 27, 10, 5, 0, 0, time.UTC)
	hooks := []HookRecord{
		{Ts: stopTs, Session: "sess-stop", Event: apmconfig.EventStop, Agent: "coder"},
	}

	recs := MergeSessions([]ParsedSession{session}, hooks)

	var stop *MergedRecord
	for i := range recs {
		if recs[i].Kind == RecordKindStop {
			stop = &recs[i]
			break
		}
	}
	if stop == nil {
		t.Fatal("expected a stop record, got none")
	}
	if stop.SessionID != "sess-stop" {
		t.Errorf("SessionID = %q, want sess-stop", stop.SessionID)
	}
	if stop.Agent != "coder" {
		t.Errorf("Agent = %q, want coder", stop.Agent)
	}
	if !stop.PreToolTs.Equal(stopTs) {
		t.Errorf("PreToolTs = %v, want %v", stop.PreToolTs, stopTs)
	}
}

func promptEvent(ts time.Time) EventEntry {
	return EventEntry{Ts: ts, Event: apmconfig.EventUserPromptSubmit}
}

func TestPairPromptsWithTimeline(t *testing.T) {
	t1 := time.Unix(1000, 0).UTC()
	t2 := time.Unix(2000, 0).UTC()
	t3 := time.Unix(3000, 0).UTC()

	tests := []struct {
		name    string
		details []SessionDetail
		want    []string
	}{
		{
			name:    "empty",
			details: nil,
			want:    []string{},
		},
		{
			name: "1:1 match",
			details: []SessionDetail{
				{
					SessionMetric: SessionMetric{},
					PromptHistory: []string{"p1", "p2"}, // oldest-first
					Timeline:      []EventEntry{promptEvent(t1), promptEvent(t2)},
				},
			},
			want: []string{"p1", "p2"}, // p1 paired with t1, p2 with t2
		},
		{
			name: "more prompts than events",
			details: []SessionDetail{
				{
					PromptHistory: []string{"p1", "p2", "p3"}, // oldest-first
					Timeline:      []EventEntry{promptEvent(t1)},
				},
			},
			// p1 paired with t1, p2 and p3 get zero ts → sort before p1
			want: []string{"p2", "p3", "p1"},
		},
		{
			name: "multiple agents interleaved by timestamp",
			details: []SessionDetail{
				{
					PromptHistory: []string{"a1", "a2"},
					Timeline:      []EventEntry{promptEvent(t1), promptEvent(t3)},
				},
				{
					PromptHistory: []string{"b1"},
					Timeline:      []EventEntry{promptEvent(t2)},
				},
			},
			// a1→t1, a2→t3, b1→t2 → sorted oldest-first: a1(t1), b1(t2), a2(t3)
			want: []string{"a1", "b1", "a2"},
		},
		{
			name: "equal timestamps preserve input order",
			details: []SessionDetail{
				{
					PromptHistory: []string{"p1", "p2"},
					Timeline:      []EventEntry{promptEvent(t1), promptEvent(t1)},
				},
			},
			// both get t1; tiebreak by seq asc → p1(seq=0) before p2(seq=1)
			want: []string{"p1", "p2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := pairPromptsWithTimeline(tc.details)
			if len(got) != len(tc.want) {
				t.Fatalf("len=%d, want %d: got %v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestMergeSessions_MalformedPrompt verifies that a Prompt with corrupt Data
// is skipped and a slog.Warn is emitted with kind=prompt and session=<sid>.
func TestMergeSessions_MalformedPrompt(t *testing.T) {
	buf, restore := testutil.CaptureSlog(t)
	defer restore()

	session := makeSession("sess-bad-prompt", []SessionMessage{
		{Kind: MessageKindPrompt, Data: json.RawMessage(`not-valid-json`)},
	})
	recs := MergeSessions([]ParsedSession{session}, nil)

	if len(recs) != 0 {
		t.Errorf("expected 0 records (prompt skipped), got %d", len(recs))
	}
	log := buf.String()
	if !strings.Contains(log, "skipped malformed merge record") {
		t.Errorf("expected warn log 'skipped malformed merge record', got: %s", log)
	}
	if !strings.Contains(log, "kind=prompt") {
		t.Errorf("expected kind=prompt in log, got: %s", log)
	}
	if !strings.Contains(log, "session=sess-bad-prompt") {
		t.Errorf("expected session=sess-bad-prompt in log, got: %s", log)
	}
}

// TestMergeSessions_MalformedAssistantMessage verifies that an AssistantMessage
// with corrupt Data is skipped and a slog.Warn is emitted.
func TestMergeSessions_MalformedAssistantMessage(t *testing.T) {
	buf, restore := testutil.CaptureSlog(t)
	defer restore()

	session := makeSession("sess-bad-asst", []SessionMessage{
		{Kind: MessageKindAssistantMessage, Data: json.RawMessage(`not-valid-json`)},
	})
	recs := MergeSessions([]ParsedSession{session}, nil)

	if len(recs) != 0 {
		t.Errorf("expected 0 records (assistant message skipped), got %d", len(recs))
	}
	log := buf.String()
	if !strings.Contains(log, "skipped malformed merge record") {
		t.Errorf("expected warn log 'skipped malformed merge record', got: %s", log)
	}
	if !strings.Contains(log, "kind=assistantMessage") {
		t.Errorf("expected kind=assistantMessage in log, got: %s", log)
	}
	if !strings.Contains(log, "session=sess-bad-asst") {
		t.Errorf("expected session=sess-bad-asst in log, got: %s", log)
	}
}

// TestMergeSessions_MalformedToolUse verifies that a toolUse ContentItem with
// corrupt Data is skipped and a slog.Warn is emitted.
func TestMergeSessions_MalformedToolUse(t *testing.T) {
	buf, restore := testutil.CaptureSlog(t)
	defer restore()

	// ci.Data is a JSON string — valid JSON but not a ToolUseData object.
	ad := AssistantData{
		MessageID: "msg-bad-tu",
		Content:   []ContentItem{{Kind: ContentKindToolUse, Data: json.RawMessage(`"not-an-object"`)}},
	}
	session := makeSession("sess-bad-tu", []SessionMessage{
		{Kind: MessageKindAssistantMessage, Data: mustJSON(ad)},
	})
	recs := MergeSessions([]ParsedSession{session}, nil)

	if len(recs) != 0 {
		t.Errorf("expected 0 records (toolUse skipped), got %d", len(recs))
	}
	log := buf.String()
	if !strings.Contains(log, "skipped malformed merge record") {
		t.Errorf("expected warn log 'skipped malformed merge record', got: %s", log)
	}
	if !strings.Contains(log, "kind=toolUse") {
		t.Errorf("expected kind=toolUse in log, got: %s", log)
	}
	if !strings.Contains(log, "session=sess-bad-tu") {
		t.Errorf("expected session=sess-bad-tu in log, got: %s", log)
	}
}

// TestMergeSessions_MultiContentPrompt verifies that a Prompt with multiple
// text ContentItems concatenates correctly (guards the strings.Builder rewrite).
func TestMergeSessions_MultiContentPrompt(t *testing.T) {
	pd := PromptData{
		MessageID: "msg-multi",
		Content: []ContentItem{
			{Kind: ContentKindText, Data: mustJSON("hello ")},
			{Kind: ContentKindText, Data: mustJSON("world")},
		},
		Meta: PromptMeta{Timestamp: 1745744400},
	}
	session := makeSession("sess-multi", []SessionMessage{
		{Kind: MessageKindPrompt, Data: mustJSON(pd)},
	})
	recs := MergeSessions([]ParsedSession{session}, nil)

	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].PromptText != "hello world" {
		t.Errorf("PromptText = %q, want %q", recs[0].PromptText, "hello world")
	}
}

// TestMergeSessions_EventConstantPreToolUse verifies that hook records with
// Event == apmconfig.EventPreToolUse are correctly matched and populate PreToolTs.
func TestMergeSessions_EventConstantPreToolUse(t *testing.T) {
	session := makeSession("sess-const", []SessionMessage{
		toolUseMsg("tu-1", "bash", map[string]string{"cmd": "ls"}),
		toolResultMsg("tu-1", "success"),
	})

	preTs := time.Date(2026, 4, 27, 10, 19, 42, 0, time.UTC)
	postTs := time.Date(2026, 4, 27, 10, 19, 43, 0, time.UTC)
	hooks := []HookRecord{
		{Ts: preTs, Session: "sess-const", Event: apmconfig.EventPreToolUse, Agent: "test-agent", Tool: "bash"},
		{Ts: postTs, Session: "sess-const", Event: apmconfig.EventPostToolUse, Agent: "test-agent", Tool: "bash", ShellExitStatus: "0"},
	}

	recs := MergeSessions([]ParsedSession{session}, hooks)

	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}

	tu := recs[0]
	if tu.Kind != RecordKindToolUse {
		t.Errorf("recs[0].Kind = %q, want toolUse", tu.Kind)
	}
	if !tu.PreToolTs.Equal(preTs) {
		t.Errorf("PreToolTs = %v, want %v", tu.PreToolTs, preTs)
	}
	if tu.Agent != "test-agent" {
		t.Errorf("Agent = %q, want test-agent", tu.Agent)
	}
}

// TestKindConstants_WireFormat guards that const values equal the previous
// literals so no future rename silently changes the wire format.
func TestKindConstants_WireFormat(t *testing.T) {
	if RecordKindPrompt != "prompt" {
		t.Errorf("RecordKindPrompt = %q, want %q", RecordKindPrompt, "prompt")
	}
	if RecordKindToolUse != "toolUse" {
		t.Errorf("RecordKindToolUse = %q, want %q", RecordKindToolUse, "toolUse")
	}
	if RecordKindToolResult != "toolResult" {
		t.Errorf("RecordKindToolResult = %q, want %q", RecordKindToolResult, "toolResult")
	}
	if RecordKindAssistantText != "assistantText" {
		t.Errorf("RecordKindAssistantText = %q, want %q", RecordKindAssistantText, "assistantText")
	}
	if RecordKindSessionMeta != "sessionMeta" {
		t.Errorf("RecordKindSessionMeta = %q, want %q", RecordKindSessionMeta, "sessionMeta")
	}
	if RecordKindAgentSpawn != "agentSpawn" {
		t.Errorf("RecordKindAgentSpawn = %q, want %q", RecordKindAgentSpawn, "agentSpawn")
	}
	if RecordKindStop != "stop" {
		t.Errorf("RecordKindStop = %q, want %q", RecordKindStop, "stop")
	}
	if MessageKindPrompt != "Prompt" {
		t.Errorf("MessageKindPrompt = %q, want %q", MessageKindPrompt, "Prompt")
	}
	if MessageKindAssistantMessage != "AssistantMessage" {
		t.Errorf("MessageKindAssistantMessage = %q, want %q", MessageKindAssistantMessage, "AssistantMessage")
	}
	if MessageKindToolResults != "ToolResults" {
		t.Errorf("MessageKindToolResults = %q, want %q", MessageKindToolResults, "ToolResults")
	}
	if ContentKindText != "text" {
		t.Errorf("ContentKindText = %q, want %q", ContentKindText, "text")
	}
	if ContentKindToolUse != "toolUse" {
		t.Errorf("ContentKindToolUse = %q, want %q", ContentKindToolUse, "toolUse")
	}
	if ContentKindToolResult != "toolResult" {
		t.Errorf("ContentKindToolResult = %q, want %q", ContentKindToolResult, "toolResult")
	}
	if ToolStatusSuccess != "success" {
		t.Errorf("ToolStatusSuccess = %q, want %q", ToolStatusSuccess, "success")
	}
	if ToolStatusError != "error" {
		t.Errorf("ToolStatusError = %q, want %q", ToolStatusError, "error")
	}
}

// --- MergeSessionDetails tests ---

func makeDetail(id, agent, agentKey, title, cwd string, start, end, last time.Time, toolCalls, prompts int) SessionDetail {
	return SessionDetail{
		SessionMetric: SessionMetric{
			ID:           id,
			Agent:        agent,
			AgentKey:     agentKey,
			Title:        title,
			Cwd:          cwd,
			StartTime:    start,
			EndTime:      end,
			LastActivity: last,
			ToolCalls:    toolCalls,
			Prompts:      prompts,
		},
	}
}

// TestMergeSessionDetails_Empty verifies that an empty slice returns zero-value
// SessionDetail and nil AgentRef slice.
func TestMergeSessionDetails_Empty(t *testing.T) {
	merged, refs := MergeSessionDetails(nil)
	if merged.ID != "" {
		t.Errorf("ID = %q, want empty", merged.ID)
	}
	if refs != nil {
		t.Errorf("refs = %v, want nil", refs)
	}
}

// TestMergeSessionDetails_Single verifies that a single SessionDetail is
// returned with Agent="(all)" and one AgentRef.
func TestMergeSessionDetails_Single(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	d := makeDetail("sid-1", "coder", "sid-1|coder", "My Title", "/work", t0, t1, t1, 3, 2)

	merged, refs := MergeSessionDetails([]SessionDetail{d})

	if merged.ID != "sid-1" {
		t.Errorf("ID = %q, want sid-1", merged.ID)
	}
	if merged.Agent != "(all)" {
		t.Errorf("Agent = %q, want (all)", merged.Agent)
	}
	if merged.Title != "My Title" {
		t.Errorf("Title = %q, want My Title", merged.Title)
	}
	if merged.ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3", merged.ToolCalls)
	}
	if merged.Prompts != 2 {
		t.Errorf("Prompts = %d, want 2", merged.Prompts)
	}
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1", len(refs))
	}
	if refs[0].Agent != "coder" || refs[0].AgentKey != "sid-1|coder" {
		t.Errorf("refs[0] = %+v, want {coder sid-1|coder}", refs[0])
	}
}

// TestMergeSessionDetails_TwoAgents verifies the full merge of two agents:
// timestamps (min start, max end/lastActivity), title/cwd from most recent,
// summed ToolCalls/Prompts, and timeline sorted by Ts.
func TestMergeSessionDetails_TwoAgents(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)

	d1 := makeDetail("sid-2", "lead", "sid-2|lead", "Old Title", "/old", t0, t2, t2, 2, 1)
	d2 := makeDetail("sid-2", "coder", "sid-2|coder", "New Title", "/new", t1, t3, t3, 5, 3)

	// Add timeline entries out of order across agents.
	d1.Timeline = []EventEntry{{Ts: t2, Event: "postToolUse"}, {Ts: t0, Event: "agentSpawn"}}
	d2.Timeline = []EventEntry{{Ts: t1, Event: "userPromptSubmit"}, {Ts: t3, Event: "stop"}}

	merged, refs := MergeSessionDetails([]SessionDetail{d1, d2})

	// ID and Agent
	if merged.ID != "sid-2" {
		t.Errorf("ID = %q, want sid-2", merged.ID)
	}
	if merged.Agent != "(all)" {
		t.Errorf("Agent = %q, want (all)", merged.Agent)
	}

	// Timestamps
	if !merged.StartTime.Equal(t0) {
		t.Errorf("StartTime = %v, want %v (min)", merged.StartTime, t0)
	}
	if !merged.EndTime.Equal(t3) {
		t.Errorf("EndTime = %v, want %v (max)", merged.EndTime, t3)
	}
	if !merged.LastActivity.Equal(t3) {
		t.Errorf("LastActivity = %v, want %v (max)", merged.LastActivity, t3)
	}

	// Title/Cwd from most recent (d2 has later LastActivity)
	if merged.Title != "New Title" {
		t.Errorf("Title = %q, want New Title", merged.Title)
	}
	if merged.Cwd != "/new" {
		t.Errorf("Cwd = %q, want /new", merged.Cwd)
	}

	// Sums
	if merged.ToolCalls != 7 {
		t.Errorf("ToolCalls = %d, want 7", merged.ToolCalls)
	}
	if merged.Prompts != 4 {
		t.Errorf("Prompts = %d, want 4", merged.Prompts)
	}

	// Timeline sorted by Ts ascending
	if len(merged.Timeline) != 4 {
		t.Fatalf("len(Timeline) = %d, want 4", len(merged.Timeline))
	}
	wantOrder := []time.Time{t0, t1, t2, t3}
	for i, ev := range merged.Timeline {
		if !ev.Ts.Equal(wantOrder[i]) {
			t.Errorf("Timeline[%d].Ts = %v, want %v", i, ev.Ts, wantOrder[i])
		}
	}

	// AgentRefs in input order
	if len(refs) != 2 {
		t.Fatalf("len(refs) = %d, want 2", len(refs))
	}
	if refs[0].Agent != "lead" {
		t.Errorf("refs[0].Agent = %q, want lead", refs[0].Agent)
	}
	if refs[1].Agent != "coder" {
		t.Errorf("refs[1].Agent = %q, want coder", refs[1].Agent)
	}
}

// TestMergeSessionDetails_AgentRefOrdering verifies that AgentRef slice
// preserves input order and carries correct AgentKey values.
func TestMergeSessionDetails_AgentRefOrdering(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	agents := []struct{ agent, key string }{
		{"alpha", "sid-3|alpha"},
		{"beta", "sid-3|beta"},
		{"gamma", "sid-3|gamma"},
	}
	details := make([]SessionDetail, len(agents))
	for i, a := range agents {
		details[i] = makeDetail("sid-3", a.agent, a.key, "", "", t0, t0, t0, 0, 0)
	}

	_, refs := MergeSessionDetails(details)

	if len(refs) != 3 {
		t.Fatalf("len(refs) = %d, want 3", len(refs))
	}
	for i, a := range agents {
		if refs[i].Agent != a.agent {
			t.Errorf("refs[%d].Agent = %q, want %q", i, refs[i].Agent, a.agent)
		}
		if refs[i].AgentKey != a.key {
			t.Errorf("refs[%d].AgentKey = %q, want %q", i, refs[i].AgentKey, a.key)
		}
	}
}

// TestMergeSessionDetails_PairPromptsWithTimeline verifies that PromptHistory
// is correctly paired with timeline events across two agents and sorted by
// timestamp.
func TestMergeSessionDetails_PairPromptsWithTimeline(t *testing.T) {
	t1 := time.Unix(1000, 0).UTC()
	t2 := time.Unix(2000, 0).UTC()
	t3 := time.Unix(3000, 0).UTC()

	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	d1 := makeDetail("sid-4", "lead", "sid-4|lead", "", "", base, base, base, 0, 2)
	d1.PromptHistory = []string{"lead-p1", "lead-p2"}
	d1.Timeline = []EventEntry{
		{Ts: t1, Event: apmconfig.EventUserPromptSubmit},
		{Ts: t3, Event: apmconfig.EventUserPromptSubmit},
	}

	d2 := makeDetail("sid-4", "coder", "sid-4|coder", "", "", base, base, base, 0, 1)
	d2.PromptHistory = []string{"coder-p1"}
	d2.Timeline = []EventEntry{
		{Ts: t2, Event: apmconfig.EventUserPromptSubmit},
	}

	merged, _ := MergeSessionDetails([]SessionDetail{d1, d2})

	// Expected order by timestamp: lead-p1(t1), coder-p1(t2), lead-p2(t3)
	want := []string{"lead-p1", "coder-p1", "lead-p2"}
	if len(merged.PromptHistory) != len(want) {
		t.Fatalf("len(PromptHistory) = %d, want %d: %v", len(merged.PromptHistory), len(want), merged.PromptHistory)
	}
	for i, p := range want {
		if merged.PromptHistory[i] != p {
			t.Errorf("PromptHistory[%d] = %q, want %q", i, merged.PromptHistory[i], p)
		}
	}
}
