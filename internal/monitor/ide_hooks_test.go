package monitor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

func TestAppendIDEHookRecordsAddsPromptToolDurationAndStop(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 5, 3, 4, 0, 0, 0, time.UTC)
	sessions := []IDEParsedSession{{
		SessionID:          "ide-sess",
		Title:              "IDE Session",
		WorkspaceDirectory: "/repo",
		CreatedAt:          createdAt,
		PromptTexts:        []string{"actual prompt"},
	}}
	execResults := map[string]IDEExecutionResult{
		"ide-sess": {
			StartTime: createdAt,
			EndTime:   createdAt.Add(10 * time.Second),
			ToolActions: []IDEAction{{
				ActionType:        ActionReadFiles,
				ActionID:          "action-1",
				ActionState:       "Success",
				EmittedAt:         createdAt.Add(3 * time.Second).UnixMilli(),
				Input:             json.RawMessage(`{"paths":["README.md"]}`),
				Output:            json.RawMessage(`"README content"`),
				EstimatedDuration: 2 * time.Second,
			}},
		},
	}
	records := BuildIDEMergedRecords(sessions, execResults)
	hooks := []HookRecord{
		{Ts: createdAt.Add(time.Second), Event: apmconfig.EventUserPromptSubmit, Agent: "ide", Cwd: "/repo"},
		{Ts: createdAt.Add(2 * time.Second), Event: apmconfig.EventPreToolUse, Agent: "ide", Cwd: "/repo"},
		{Ts: createdAt.Add(5 * time.Second), Event: apmconfig.EventPostToolUse, Agent: "ide", Cwd: "/repo"},
		{Ts: createdAt.Add(6 * time.Second), Event: apmconfig.EventStop, Agent: "ide", Cwd: "/repo"},
	}

	records = AppendIDEHookRecords(records, sessions, hooks)
	detail, err := AggregateDetail(context.Background(), records, createdAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("AggregateDetail() error = %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(detail.Sessions))
	}
	s := detail.Sessions[0]
	if s.Prompts != 1 || len(s.PromptHistory) != 1 || s.PromptHistory[0] != "actual prompt" {
		t.Fatalf("prompts = %d history = %#v", s.Prompts, s.PromptHistory)
	}
	if s.ToolCalls != 1 {
		t.Fatalf("ToolCalls = %d, want 1", s.ToolCalls)
	}
	if s.Active {
		t.Fatal("session should be inactive after stop hook")
	}

	var foundTool bool
	for _, ev := range s.Timeline {
		if ev.Event == apmconfig.EventPreToolUse && ev.Tool == ActionReadFiles {
			foundTool = true
			if ev.InputSummary != "README.md" {
				t.Fatalf("InputSummary = %q, want README.md", ev.InputSummary)
			}
			if ev.Duration != JSONDuration(3*time.Second) {
				t.Fatalf("Duration = %s, want 3s", time.Duration(ev.Duration))
			}
		}
	}
	if !foundTool {
		t.Fatalf("tool event not found in timeline: %#v", s.Timeline)
	}
}

func TestAppendIDEHookRecordsKeepsUnmatchedPromptFallbacks(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 5, 3, 4, 0, 0, 0, time.UTC)
	sessions := []IDEParsedSession{{
		SessionID:          "ide-sess",
		Title:              "IDE Session",
		WorkspaceDirectory: "/repo",
		CreatedAt:          createdAt,
		PromptTexts:        []string{"first prompt", "second prompt"},
	}}
	records := BuildIDEMergedRecords(sessions, map[string]IDEExecutionResult{})
	hooks := []HookRecord{{
		Ts:    createdAt.Add(time.Second),
		Event: apmconfig.EventUserPromptSubmit,
		Agent: "ide",
		Cwd:   "/repo",
	}}

	records = AppendIDEHookRecords(records, sessions, hooks)
	detail, err := AggregateDetail(context.Background(), records, createdAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("AggregateDetail() error = %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(detail.Sessions))
	}
	got := detail.Sessions[0].PromptHistory
	if len(got) != 2 || got[0] != "first prompt" || got[1] != "second prompt" {
		t.Fatalf("PromptHistory = %#v", got)
	}
}

func TestAppendIDEHookRecordsSkipsMismatchedCwd(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 5, 3, 4, 0, 0, 0, time.UTC)
	sessions := []IDEParsedSession{{
		SessionID:          "ide-sess",
		Title:              "IDE Session",
		WorkspaceDirectory: "/repo",
		CreatedAt:          createdAt,
	}}
	records := BuildIDEMergedRecords(sessions, map[string]IDEExecutionResult{})
	hooks := []HookRecord{{Ts: createdAt.Add(time.Second), Event: apmconfig.EventStop, Agent: "ide", Cwd: "/other"}}

	records = AppendIDEHookRecords(records, sessions, hooks)
	detail, err := AggregateDetail(context.Background(), records, createdAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("AggregateDetail() error = %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(detail.Sessions))
	}
	if len(detail.Sessions[0].Timeline) != 0 {
		t.Fatalf("mismatched cwd hook should not enter timeline: %#v", detail.Sessions[0].Timeline)
	}
}

func TestAppendIDEHookRecordsHookOnlyToolEventsAreLifecycleOnly(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 5, 3, 4, 0, 0, 0, time.UTC)
	sessions := []IDEParsedSession{{
		SessionID:          "ide-sess",
		Title:              "IDE Session",
		WorkspaceDirectory: "/repo",
		CreatedAt:          createdAt,
	}}
	records := BuildIDEMergedRecords(sessions, map[string]IDEExecutionResult{})
	hooks := []HookRecord{
		{Ts: createdAt.Add(time.Second), Event: apmconfig.EventPreToolUse, Agent: "ide", Cwd: "/repo"},
		{Ts: createdAt.Add(2 * time.Second), Event: apmconfig.EventPostToolUse, Agent: "ide", Cwd: "/repo"},
	}

	records = AppendIDEHookRecords(records, sessions, hooks)
	detail, err := AggregateDetail(context.Background(), records, createdAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("AggregateDetail() error = %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(detail.Sessions))
	}
	s := detail.Sessions[0]
	if s.ToolCalls != 0 {
		t.Fatalf("ToolCalls = %d, want 0 for hook-only IDE tool lifecycle events", s.ToolCalls)
	}
	if len(s.Timeline) != 2 {
		t.Fatalf("Timeline len = %d, want 2: %#v", len(s.Timeline), s.Timeline)
	}
	for _, ev := range s.Timeline {
		if ev.Tool != "" {
			t.Fatalf("hook-only lifecycle event invented tool %q: %#v", ev.Tool, ev)
		}
	}
}

func TestAppendIDEHookRecordsMatchesExecutionStartBeforeSessionCreatedAt(t *testing.T) {
	t.Parallel()
	newExecStart := time.Date(2026, 5, 4, 0, 59, 40, 0, time.UTC)
	oldExecStart := newExecStart.Add(-7 * time.Hour)
	sessions := []IDEParsedSession{
		{
			SessionID:          "old-sess",
			Title:              "Old IDE Session",
			WorkspaceDirectory: "/repo",
			CreatedAt:          oldExecStart,
			PromptTexts:        []string{"old prompt"},
		},
		{
			SessionID:          "new-sess",
			Title:              "New IDE Session",
			WorkspaceDirectory: "/repo",
			CreatedAt:          newExecStart.Add(10 * time.Second),
			PromptTexts:        []string{"new prompt"},
		},
	}
	execResults := map[string]IDEExecutionResult{
		"old-sess": {StartTime: oldExecStart, EndTime: oldExecStart.Add(30 * time.Second), Executions: 1},
		"new-sess": {StartTime: newExecStart, EndTime: newExecStart.Add(30 * time.Second), Executions: 1},
	}
	records := BuildIDEMergedRecords(sessions, execResults)
	hooks := []HookRecord{{
		Ts:    newExecStart.Add(2 * time.Second),
		Event: apmconfig.EventUserPromptSubmit,
		Agent: "ide",
		Cwd:   "/repo",
	}}

	records = AppendIDEHookRecords(records, sessions, hooks)
	var promptRecords []MergedRecord
	for _, r := range records {
		if r.Kind == RecordKindPrompt {
			promptRecords = append(promptRecords, r)
		}
	}
	if len(promptRecords) != 1 {
		t.Fatalf("prompt records len = %d, want 1: %#v", len(promptRecords), promptRecords)
	}
	prompt := promptRecords[0]
	if prompt.SessionID != "new-sess" || prompt.PromptText != "new prompt" {
		t.Fatalf("prompt record matched wrong session: %#v", prompt)
	}
}

func TestAggregateDetailBuildsChangesFromIDEFileActions(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 5, 3, 4, 0, 0, 0, time.UTC)
	sessions := []IDEParsedSession{{
		SessionID:          "ide-sess",
		Title:              "IDE Session",
		WorkspaceDirectory: "/repo",
		CreatedAt:          createdAt,
	}}
	execResults := map[string]IDEExecutionResult{
		"ide-sess": {
			StartTime: createdAt,
			EndTime:   createdAt.Add(10 * time.Second),
			ToolActions: []IDEAction{
				{
					ActionType:        ActionCreate,
					ActionID:          "create-1",
					ActionState:       "Success",
					EmittedAt:         createdAt.Add(time.Second).UnixMilli(),
					Input:             json.RawMessage(`{"file":"tmp-test.txt","originalContent":"","modifiedContent":"hello\n"}`),
					EstimatedDuration: time.Second,
				},
				{
					ActionType:        ActionDelete,
					ActionID:          "delete-1",
					ActionState:       "Success",
					EmittedAt:         createdAt.Add(3 * time.Second).UnixMilli(),
					Input:             json.RawMessage(`{"file":"tmp-test.txt","why":"cleanup"}`),
					EstimatedDuration: time.Second,
				},
			},
		},
	}

	records := BuildIDEMergedRecords(sessions, execResults)
	detail, err := AggregateDetail(context.Background(), records, createdAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("AggregateDetail() error = %v", err)
	}
	s := detail.Sessions[0]
	if s.FilesChanged != 1 {
		t.Fatalf("FilesChanged = %d, want 1", s.FilesChanged)
	}
	if len(s.Changes) != 2 {
		t.Fatalf("Changes len = %d, want 2: %#v", len(s.Changes), s.Changes)
	}
	if s.Changes[0].Command != CommandCreate || s.Changes[0].Content != "hello\n" {
		t.Fatalf("create change = %#v", s.Changes[0])
	}
	if s.Changes[1].Command != CommandDelete || s.Changes[1].Purpose != "cleanup" {
		t.Fatalf("delete change = %#v", s.Changes[1])
	}
}

func TestAggregateDetailBuildsChangesFromIDEWriteAction(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 5, 3, 4, 0, 0, 0, time.UTC)
	sessions := []IDEParsedSession{{
		SessionID:          "ide-sess",
		Title:              "IDE Session",
		WorkspaceDirectory: "/repo",
		CreatedAt:          createdAt,
	}}
	execResults := map[string]IDEExecutionResult{
		"ide-sess": {
			StartTime: createdAt,
			EndTime:   createdAt.Add(10 * time.Second),
			ToolActions: []IDEAction{{
				ActionType:        ActionWrite,
				ActionID:          "write-1",
				ActionState:       "Success",
				EmittedAt:         createdAt.Add(time.Second).UnixMilli(),
				Input:             json.RawMessage(`{"file":"a.txt","originalContent":"old\n","modifiedContent":"new\n"}`),
				EstimatedDuration: time.Second,
			}},
		},
	}

	records := BuildIDEMergedRecords(sessions, execResults)
	detail, err := AggregateDetail(context.Background(), records, createdAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("AggregateDetail() error = %v", err)
	}
	s := detail.Sessions[0]
	if s.FilesChanged != 1 || len(s.Changes) != 1 {
		t.Fatalf("files=%d changes=%#v", s.FilesChanged, s.Changes)
	}
	if s.Changes[0].Command != CommandStrReplace || s.Changes[0].OldStr != "old\n" || s.Changes[0].NewStr != "new\n" {
		t.Fatalf("write change = %#v", s.Changes[0])
	}
}

func TestAggregateDetailSkipsRejectedIDEFileActionsForChanges(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 5, 3, 4, 0, 0, 0, time.UTC)
	sessions := []IDEParsedSession{{
		SessionID:          "ide-sess",
		Title:              "IDE Session",
		WorkspaceDirectory: "/repo",
		CreatedAt:          createdAt,
	}}
	execResults := map[string]IDEExecutionResult{
		"ide-sess": {
			StartTime: createdAt,
			EndTime:   createdAt.Add(10 * time.Second),
			ToolActions: []IDEAction{{
				ActionType:        ActionCreate,
				ActionID:          "create-1",
				ActionState:       "Rejected",
				EmittedAt:         createdAt.Add(time.Second).UnixMilli(),
				Input:             json.RawMessage(`{"file":"tmp-test.txt","modifiedContent":"hello\n"}`),
				EstimatedDuration: time.Second,
			}},
		},
	}

	records := BuildIDEMergedRecords(sessions, execResults)
	detail, err := AggregateDetail(context.Background(), records, createdAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("AggregateDetail() error = %v", err)
	}
	s := detail.Sessions[0]
	if s.FilesChanged != 0 || len(s.Changes) != 0 {
		t.Fatalf("rejected action should not create Changes: files=%d changes=%#v", s.FilesChanged, s.Changes)
	}
	if len(s.Timeline) != 1 || !s.Timeline[0].IsError {
		t.Fatalf("timeline should retain rejected action as error: %#v", s.Timeline)
	}
}
