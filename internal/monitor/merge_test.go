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
		Content:   []ContentItem{{Kind: "text", Data: mustJSON(text)}},
		Meta:      PromptMeta{Timestamp: ts},
	}
	return SessionMessage{Kind: "Prompt", Data: mustJSON(pd)}
}

func toolUseMsg(id, name string, input any) SessionMessage {
	tu := ToolUseData{ToolUseID: id, Name: name, Input: mustJSON(input)}
	ad := AssistantData{
		MessageID: "msg-2",
		Content:   []ContentItem{{Kind: "toolUse", Data: mustJSON(tu)}},
	}
	return SessionMessage{Kind: "AssistantMessage", Data: mustJSON(ad)}
}

func toolResultMsg(id, status string) SessionMessage {
	tr := ToolResultData{
		ToolUseID: id,
		Status:    status,
		Content:   []ContentItem{{Kind: "text", Data: mustJSON("ok")}},
	}
	trs := struct {
		Content []ContentItem `json:"content"`
	}{Content: []ContentItem{{Kind: "toolResult", Data: mustJSON(tr)}}}
	return SessionMessage{Kind: "ToolResults", Data: mustJSON(trs)}
}

func toolResultErrMsg(id, errText string) SessionMessage {
	tr := ToolResultData{
		ToolUseID: id,
		Status:    "error",
		Content:   []ContentItem{{Kind: "text", Data: mustJSON(errText)}},
	}
	trs := struct {
		Content []ContentItem `json:"content"`
	}{Content: []ContentItem{{Kind: "toolResult", Data: mustJSON(tr)}}}
	return SessionMessage{Kind: "ToolResults", Data: mustJSON(trs)}
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
	if prompt.Kind != "prompt" {
		t.Errorf("recs[0].Kind = %q, want prompt", prompt.Kind)
	}
	if prompt.PromptText != "hello" {
		t.Errorf("PromptText = %q, want hello", prompt.PromptText)
	}
	if !prompt.PreToolTs.IsZero() || !prompt.PostToolTs.IsZero() || prompt.Agent != "" {
		t.Error("hook fields should be zero for sessions-only")
	}

	tu := recs[1]
	if tu.Kind != "toolUse" {
		t.Errorf("recs[1].Kind = %q, want toolUse", tu.Kind)
	}
	if tu.ToolName != "read" {
		t.Errorf("ToolName = %q, want read", tu.ToolName)
	}
	if !tu.PreToolTs.IsZero() || tu.Agent != "" {
		t.Error("hook fields should be zero for sessions-only toolUse")
	}

	tr := recs[2]
	if tr.Kind != "toolResult" {
		t.Errorf("recs[2].Kind = %q, want toolResult", tr.Kind)
	}
	if tr.ToolStatus != "success" {
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
	if tu.Kind != "toolUse" {
		t.Errorf("recs[0].Kind = %q, want toolUse", tu.Kind)
	}
	if !tu.PreToolTs.Equal(preTs) {
		t.Errorf("PreToolTs = %v, want %v", tu.PreToolTs, preTs)
	}
	if tu.Agent != "orchestrator" {
		t.Errorf("Agent = %q, want orchestrator", tu.Agent)
	}

	tr := recs[1]
	if tr.Kind != "toolResult" {
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
	if tu1.Kind != "toolUse" {
		t.Errorf("recs[0].Kind = %q, want toolUse", tu1.Kind)
	}
	if !tu1.PreToolTs.Equal(preTs) {
		t.Errorf("recs[0].PreToolTs = %v, want %v (first toolUse gets hook[0])", tu1.PreToolTs, preTs)
	}

	tu2 := recs[2]
	if tu2.Kind != "toolUse" {
		t.Errorf("recs[2].Kind = %q, want toolUse", tu2.Kind)
	}
	if !tu2.PreToolTs.IsZero() {
		t.Errorf("recs[2].PreToolTs should be zero (no hook for second toolUse), got %v", tu2.PreToolTs)
	}

	tr2 := recs[3]
	if tr2.Kind != "toolResult" {
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
		if recs[i].Kind == "sessionMeta" {
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
		if r.Kind == "sessionMeta" {
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
		if recs[i].Kind == "sessionMeta" {
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
		if recs[i].Kind == "sessionMeta" {
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
	if tr.ToolStatus != "error" {
		t.Errorf("ToolStatus = %q, want error", tr.ToolStatus)
	}
	if tr.ErrorDetail != "command not found" {
		t.Errorf("ErrorDetail = %q, want 'command not found'", tr.ErrorDetail)
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
			got := pairPromptsWithTimeline(tc.details)
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
		{Kind: "Prompt", Data: json.RawMessage(`not-valid-json`)},
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
		{Kind: "AssistantMessage", Data: json.RawMessage(`not-valid-json`)},
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
		Content:   []ContentItem{{Kind: "toolUse", Data: json.RawMessage(`"not-an-object"`)}},
	}
	session := makeSession("sess-bad-tu", []SessionMessage{
		{Kind: "AssistantMessage", Data: mustJSON(ad)},
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
			{Kind: "text", Data: mustJSON("hello ")},
			{Kind: "text", Data: mustJSON("world")},
		},
		Meta: PromptMeta{Timestamp: 1745744400},
	}
	session := makeSession("sess-multi", []SessionMessage{
		{Kind: "Prompt", Data: mustJSON(pd)},
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
	if tu.Kind != "toolUse" {
		t.Errorf("recs[0].Kind = %q, want toolUse", tu.Kind)
	}
	if !tu.PreToolTs.Equal(preTs) {
		t.Errorf("PreToolTs = %v, want %v", tu.PreToolTs, preTs)
	}
	if tu.Agent != "test-agent" {
		t.Errorf("Agent = %q, want test-agent", tu.Agent)
	}
}
