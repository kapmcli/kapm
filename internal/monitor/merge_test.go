package monitor

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
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
			CreatedAt: "2026-04-27T10:00:00Z",
			UpdatedAt: "2026-04-27T10:01:00Z",
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
					PromptHistory: []string{"p2", "p1"}, // newest-first
					Timeline:      []EventEntry{promptEvent(t1), promptEvent(t2)},
				},
			},
			want: []string{"p2", "p1"}, // p2 paired with t2 (newer), p1 with t1
		},
		{
			name: "more prompts than events",
			details: []SessionDetail{
				{
					PromptHistory: []string{"p3", "p2", "p1"}, // newest-first
					Timeline:      []EventEntry{promptEvent(t1)},
				},
			},
			// p1 paired with t1, p2 and p3 get zero ts → sort after p1
			want: []string{"p1", "p3", "p2"},
		},
		{
			name: "multiple agents interleaved by timestamp",
			details: []SessionDetail{
				{
					PromptHistory: []string{"a2", "a1"},
					Timeline:      []EventEntry{promptEvent(t1), promptEvent(t3)},
				},
				{
					PromptHistory: []string{"b1"},
					Timeline:      []EventEntry{promptEvent(t2)},
				},
			},
			// a1→t1, a2→t3, b1→t2 → sorted newest-first: a2(t3), b1(t2), a1(t1)
			want: []string{"a2", "b1", "a1"},
		},
		{
			name: "equal timestamps preserve input order",
			details: []SessionDetail{
				{
					PromptHistory: []string{"p2", "p1"},
					Timeline:      []EventEntry{promptEvent(t1), promptEvent(t1)},
				},
			},
			// both get t1; tiebreak by seq desc → p2(seq=1) before p1(seq=0)
			want: []string{"p2", "p1"},
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
