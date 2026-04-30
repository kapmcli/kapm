package monitor

import (
	"context"
	"testing"
	"time"
)

func TestAggregateDetail_StopMarksSessionInactive(t *testing.T) {
	// A session with a stop record should be inactive regardless of timing.
	now := time.Date(2026, 4, 27, 10, 1, 0, 0, time.UTC)
	stopTs := now.Add(-10 * time.Second) // well within the 5-min active window

	records := []MergedRecord{
		{
			SessionID: "sess-1",
			Kind:      RecordKindToolUse,
			ToolName:  "read",
			ToolUseID: "tu-1",
			Agent:     "coder",
			PreToolTs: now.Add(-30 * time.Second),
			Cwd:       "/tmp",
		},
		{
			SessionID: "sess-1",
			Kind:      RecordKindStop,
			Agent:     "coder",
			PreToolTs: stopTs,
			Cwd:       "/tmp",
		},
	}

	dm, err := AggregateDetail(context.Background(), records, now)
	if err != nil {
		t.Fatalf("AggregateDetail: %v", err)
	}
	if len(dm.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(dm.Sessions))
	}
	if dm.Sessions[0].Active {
		t.Error("session with stop record should be inactive")
	}
}

func TestAggregateDetail_NoStopUsesTimeout(t *testing.T) {
	// A session without a stop record should use the 5-min timeout.
	now := time.Date(2026, 4, 27, 10, 1, 0, 0, time.UTC)

	records := []MergedRecord{
		{
			SessionID: "sess-2",
			Kind:      RecordKindToolUse,
			ToolName:  "read",
			ToolUseID: "tu-1",
			Agent:     "coder",
			PreToolTs: now.Add(-30 * time.Second), // 30s ago → within 5-min window
			Cwd:       "/tmp",
		},
	}

	dm, err := AggregateDetail(context.Background(), records, now)
	if err != nil {
		t.Fatalf("AggregateDetail: %v", err)
	}
	if len(dm.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(dm.Sessions))
	}
	if !dm.Sessions[0].Active {
		t.Error("session without stop record and recent activity should be active")
	}
}

func TestAggregateDetail_NoStopTimedOut(t *testing.T) {
	// A session without a stop record and old activity should be inactive via timeout.
	now := time.Date(2026, 4, 27, 10, 30, 0, 0, time.UTC)

	records := []MergedRecord{
		{
			SessionID: "sess-3",
			Kind:      RecordKindToolUse,
			ToolName:  "read",
			ToolUseID: "tu-1",
			Agent:     "coder",
			PreToolTs: now.Add(-10 * time.Minute), // 10 min ago → beyond 5-min window
			Cwd:       "/tmp",
		},
	}

	dm, err := AggregateDetail(context.Background(), records, now)
	if err != nil {
		t.Fatalf("AggregateDetail: %v", err)
	}
	if len(dm.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(dm.Sessions))
	}
	if dm.Sessions[0].Active {
		t.Error("session without stop record and old activity should be inactive")
	}
}

func TestAggregateDetail_AgentSpawnTimeline(t *testing.T) {
	now := time.Date(2026, 4, 27, 10, 1, 0, 0, time.UTC)
	spawnTs := now.Add(-20 * time.Second)

	records := []MergedRecord{
		{
			SessionID: "sess-4",
			Kind:      RecordKindAgentSpawn,
			Agent:     "lead",
			PreToolTs: spawnTs,
			Cwd:       "/tmp",
		},
	}

	dm, err := AggregateDetail(context.Background(), records, now)
	if err != nil {
		t.Fatalf("AggregateDetail: %v", err)
	}
	if len(dm.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(dm.Sessions))
	}
	found := false
	for _, ev := range dm.Sessions[0].Timeline {
		if ev.Event == "agentSpawn" {
			found = true
			if !ev.Ts.Equal(spawnTs) {
				t.Errorf("agentSpawn Ts = %v, want %v", ev.Ts, spawnTs)
			}
		}
	}
	if !found {
		t.Error("expected agentSpawn event in timeline")
	}
}

// --- Task 5: processRecord missing RecordKind paths -------------------------

func TestProcessRecord_ToolResult_PairsWithPendingToolUse(t *testing.T) {
	pre, post := recPair("s1", "coder", "bash", 0, 2*time.Second)
	now := baseTime.Add(time.Hour)
	d := mustAggregate(t, []MergedRecord{pre, post}, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(d.Sessions))
	}
	// Matched toolResult: timeline entry must not be an error.
	tl := d.Sessions[0].Timeline
	if len(tl) != 1 {
		t.Fatalf("want 1 timeline entry, got %d", len(tl))
	}
	if tl[0].IsError {
		t.Error("matched toolResult: IsError should be false")
	}
	if tl[0].Duration != JSONDuration(2*time.Second) {
		t.Errorf("Duration: want 2s, got %v", tl[0].Duration)
	}
}

func TestProcessRecord_ToolResult_ErrorStatus(t *testing.T) {
	pre := MergedRecord{SessionID: "s1", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-1", ToolName: "bash", PreToolTs: baseTime}
	post := MergedRecord{SessionID: "s1", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-1", ToolName: "bash", PostToolTs: baseTime.Add(3 * time.Second), ToolStatus: ToolStatusError, ErrorDetail: "exit 1"}
	now := baseTime.Add(time.Hour)
	d := mustAggregate(t, []MergedRecord{pre, post}, now)
	tl := d.Sessions[0].Timeline
	if len(tl) != 1 {
		t.Fatalf("want 1 timeline entry, got %d", len(tl))
	}
	if !tl[0].IsError {
		t.Error("error toolResult: IsError should be true")
	}
	var bash ToolDetail
	for _, td := range d.Tools {
		if td.Name == "bash" {
			bash = td
		}
	}
	if bash.ErrorCount != 1 {
		t.Errorf("ErrorCount: want 1, got %d", bash.ErrorCount)
	}
}

func TestProcessRecord_ToolResult_UnmatchedIDIgnored(t *testing.T) {
	// A toolResult with a ToolUseID that has no matching toolUse is silently dropped.
	post := MergedRecord{SessionID: "s1", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "no-such-id", ToolName: "bash", PostToolTs: baseTime.Add(time.Second), ToolStatus: ToolStatusSuccess}
	now := baseTime.Add(time.Hour)
	d := mustAggregate(t, []MergedRecord{post}, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(d.Sessions))
	}
	if len(d.Sessions[0].Timeline) != 0 {
		t.Errorf("unmatched toolResult should not add timeline entry, got %v", d.Sessions[0].Timeline)
	}
}

func TestProcessRecord_SessionMeta_AccumulatesTokensAndCredits(t *testing.T) {
	now := baseTime.Add(time.Hour)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "a", Kind: RecordKindSessionMeta, TotalInputTokens: 100, TotalOutputTokens: 200, TotalCredits: 1.5},
		{SessionID: "s1", Agent: "a", Kind: RecordKindSessionMeta, TotalInputTokens: 50, TotalOutputTokens: 75, TotalCredits: 0.5},
	}
	d := mustAggregate(t, records, now)
	sm := d.Sessions[0].SessionMetric
	if sm.TotalInputTokens != 150 {
		t.Errorf("TotalInputTokens: want 150, got %d", sm.TotalInputTokens)
	}
	if sm.TotalOutputTokens != 275 {
		t.Errorf("TotalOutputTokens: want 275, got %d", sm.TotalOutputTokens)
	}
	if sm.TotalCredits != 2.0 {
		t.Errorf("TotalCredits: want 2.0, got %f", sm.TotalCredits)
	}
}

func TestProcessRecord_SessionMeta_TitleFallback(t *testing.T) {
	// Title is used when sumTitle is empty and no prompts exist.
	now := baseTime.Add(time.Hour)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "a", Kind: RecordKindSessionMeta, Title: "my session title", CreatedAt: baseTime},
	}
	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(d.Sessions))
	}
	if d.Sessions[0].Title != "my session title" {
		t.Errorf("Title: want %q, got %q", "my session title", d.Sessions[0].Title)
	}
}

func TestProcessRecord_SessionMeta_TitleNotOverriddenByPrompt(t *testing.T) {
	// When a prompt already exists, sessionMeta Title must not override it.
	// The sessionMeta CreatedAt is after the prompt so it is processed second.
	now := baseTime.Add(time.Hour)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "a", Kind: RecordKindPrompt, PromptTs: baseTime, PromptText: "first prompt"},
		{SessionID: "s1", Agent: "a", Kind: RecordKindSessionMeta, Title: "should not win", CreatedAt: baseTime.Add(time.Second)},
	}
	d := mustAggregate(t, records, now)
	if d.Sessions[0].Title != "first prompt" {
		t.Errorf("Title: want %q, got %q", "first prompt", d.Sessions[0].Title)
	}
}

func TestProcessRecord_SessionMeta_ToolCallsAccumulated(t *testing.T) {
	now := baseTime.Add(time.Hour)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "a", Kind: RecordKindSessionMeta, ToolCalls: 5},
		{SessionID: "s1", Agent: "a", Kind: RecordKindSessionMeta, ToolCalls: 3},
	}
	d := mustAggregate(t, records, now)
	if d.Sessions[0].ToolCalls != 8 {
		t.Errorf("ToolCalls: want 8, got %d", d.Sessions[0].ToolCalls)
	}
}

func TestProcessRecord_Prompt_AppendsTimeline(t *testing.T) {
	now := baseTime.Add(time.Hour)
	promptTs := baseTime.Add(5 * time.Minute)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "a", Kind: RecordKindPrompt, PromptTs: promptTs, PromptText: "hello", TurnResponse: "world"},
	}
	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(d.Sessions))
	}
	tl := d.Sessions[0].Timeline
	if len(tl) != 1 {
		t.Fatalf("want 1 timeline entry, got %d", len(tl))
	}
	if tl[0].Event != "userPromptSubmit" {
		t.Errorf("Event: want userPromptSubmit, got %q", tl[0].Event)
	}
	if !tl[0].Ts.Equal(promptTs) {
		t.Errorf("Ts: want %v, got %v", promptTs, tl[0].Ts)
	}
	ph := d.Sessions[0].PromptHistory
	if len(ph) != 1 || ph[0] != "hello" {
		t.Errorf("PromptHistory: want [hello], got %v", ph)
	}
}

func TestProcessRecord_Prompt_MultiplePromptsOrdered(t *testing.T) {
	now := baseTime.Add(time.Hour)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "a", Kind: RecordKindPrompt, PromptTs: baseTime.Add(1 * time.Minute), PromptText: "first"},
		{SessionID: "s1", Agent: "a", Kind: RecordKindPrompt, PromptTs: baseTime.Add(2 * time.Minute), PromptText: "second"},
	}
	d := mustAggregate(t, records, now)
	ph := d.Sessions[0].PromptHistory
	if len(ph) != 2 {
		t.Fatalf("want 2 prompts, got %d", len(ph))
	}
	if ph[0] != "first" || ph[1] != "second" {
		t.Errorf("PromptHistory order: want [first second], got %v", ph)
	}
}

func TestProcessRecord_AssistantText_SetsResponse(t *testing.T) {
	now := baseTime.Add(time.Hour)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "a", Kind: RecordKindAssistantText, AssistantText: "done", CreatedAt: baseTime},
	}
	d := mustAggregate(t, records, now)
	if d.Sessions[0].AssistantResponse != "done" {
		t.Errorf("AssistantResponse: want %q, got %q", "done", d.Sessions[0].AssistantResponse)
	}
}

func TestProcessRecord_AssistantText_Truncated(t *testing.T) {
	long := string(make([]byte, maxAssistantResponseLength+100))
	for i := range long {
		long = long[:i] + "x" + long[i+1:]
	}
	now := baseTime.Add(time.Hour)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "a", Kind: RecordKindAssistantText, AssistantText: long, CreatedAt: baseTime},
	}
	d := mustAggregate(t, records, now)
	got := d.Sessions[0].AssistantResponse
	if len(got) > maxAssistantResponseLength {
		t.Errorf("AssistantResponse not truncated: len=%d, want ≤%d", len(got), maxAssistantResponseLength)
	}
}

func TestProcessRecord_AssistantText_LastWins(t *testing.T) {
	// Multiple assistantText records: last one wins.
	now := baseTime.Add(time.Hour)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "a", Kind: RecordKindAssistantText, AssistantText: "first", CreatedAt: baseTime},
		{SessionID: "s1", Agent: "a", Kind: RecordKindAssistantText, AssistantText: "last", CreatedAt: baseTime.Add(time.Second)},
	}
	d := mustAggregate(t, records, now)
	if d.Sessions[0].AssistantResponse != "last" {
		t.Errorf("AssistantResponse: want %q, got %q", "last", d.Sessions[0].AssistantResponse)
	}
}

func TestFoldSessionIntoAgents_SessionCount(t *testing.T) {
	now := baseTime.Add(time.Hour)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "agentX", Kind: RecordKindPrompt, PromptTs: baseTime, PromptText: "p1"},
		{SessionID: "s2", Agent: "agentX", Kind: RecordKindPrompt, PromptTs: baseTime.Add(10 * time.Minute), PromptText: "p2"},
		{SessionID: "s3", Agent: "agentX", Kind: RecordKindPrompt, PromptTs: baseTime.Add(20 * time.Minute), PromptText: "p3"},
	}
	d := mustAggregate(t, records, now)
	byName := map[string]AgentDetail{}
	for _, a := range d.Agents {
		byName[a.Name] = a
	}
	if byName["agentX"].SessionCount != 3 {
		t.Errorf("SessionCount: want 3, got %d", byName["agentX"].SessionCount)
	}
}

func TestFoldSessionIntoAgents_PerToolAggregation(t *testing.T) {
	now := baseTime.Add(time.Hour)
	pre1, post1 := recPair("s1", "agentY", "read", 0, 4*time.Second)
	pre2, post2 := recPair("s1", "agentY", "read", 10*time.Second, 16*time.Second)
	errPre := MergedRecord{SessionID: "s1", Agent: "agentY", Kind: RecordKindToolUse, ToolUseID: "tu-err", ToolName: "read", PreToolTs: baseTime.Add(20 * time.Second)}
	errPost := MergedRecord{SessionID: "s1", Agent: "agentY", Kind: RecordKindToolResult, ToolUseID: "tu-err", ToolName: "read", PostToolTs: baseTime.Add(22 * time.Second), ToolStatus: ToolStatusError}
	records := []MergedRecord{pre1, post1, pre2, post2, errPre, errPost}
	d := mustAggregate(t, records, now)
	byName := map[string]AgentDetail{}
	for _, a := range d.Agents {
		byName[a.Name] = a
	}
	a := byName["agentY"]
	toolMap := map[string]SessionToolSummary{}
	for _, ts := range a.ToolSummary {
		toolMap[ts.Tool] = ts
	}
	read := toolMap["read"]
	if read.CallCount != 3 {
		t.Errorf("read CallCount: want 3, got %d", read.CallCount)
	}
	if read.ErrorCount != 1 {
		t.Errorf("read ErrorCount: want 1, got %d", read.ErrorCount)
	}
	if a.ToolErrorCnt != 1 {
		t.Errorf("ToolErrorCnt: want 1, got %d", a.ToolErrorCnt)
	}
}
