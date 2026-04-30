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
