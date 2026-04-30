package monitor

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const fakeProfileHash = "be6800b174065600d20a690aaef89855"

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func buildExecDir(t *testing.T) (baseDir string, execIDs map[string]struct{}) {
	t.Helper()
	base := t.TempDir()
	profileDir := filepath.Join(base, fakeProfileHash)
	sessionDir := filepath.Join(profileDir, "aabbccdd11223344aabbccdd11223344")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// index file (non-directory with "executions" key)
	writeJSON(t, filepath.Join(profileDir, "indexfile0000000000000000000000000"), IDEExecutionIndex{
		Executions: []IDEExecutionEntry{{ExecutionID: "exec-1"}},
	})

	// execution log 1: session-A
	writeJSON(t, filepath.Join(sessionDir, "hash1"), IDEExecutionLog{
		ExecutionID:   "exec-1",
		ChatSessionID: "session-A",
		StartTime:     1000,
		EndTime:       2000,
		UsageSummary:  []IDEUsageEntry{{Unit: "credit", Usage: 0.1}, {Unit: "credit", Usage: 0.05}},
	})
	// execution log 2: session-A, earlier start, later end
	writeJSON(t, filepath.Join(sessionDir, "hash2"), IDEExecutionLog{
		ExecutionID:   "exec-2",
		ChatSessionID: "session-A",
		StartTime:     500,
		EndTime:       3000,
		UsageSummary:  []IDEUsageEntry{{Unit: "credit", Usage: 0.02}},
	})

	return base, map[string]struct{}{"exec-1": {}, "exec-2": {}}
}

func approxEqual(a, b, eps float64) bool { return math.Abs(a-b) < eps }

func TestLoadIDEExecutions_Valid(t *testing.T) {
	t.Parallel()
	base, execIDs := buildExecDir(t)

	results, err := LoadIDEExecutions(context.Background(), base, execIDs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r, ok := results["session-A"]
	if !ok {
		t.Fatal("expected session-A in results")
	}
	if !approxEqual(r.TotalCredits, 0.17, 1e-9) {
		t.Errorf("TotalCredits = %v, want 0.17", r.TotalCredits)
	}
	if r.Executions != 2 {
		t.Errorf("Executions = %d, want 2", r.Executions)
	}
	if !r.StartTime.Equal(time.UnixMilli(500)) {
		t.Errorf("StartTime = %v, want %v", r.StartTime, time.UnixMilli(500))
	}
	if !r.EndTime.Equal(time.UnixMilli(3000)) {
		t.Errorf("EndTime = %v, want %v", r.EndTime, time.UnixMilli(3000))
	}
}

func TestLoadIDEExecutions_NoMatch(t *testing.T) {
	t.Parallel()
	base, _ := buildExecDir(t)

	results, err := LoadIDEExecutions(context.Background(), base, map[string]struct{}{"no-such-id": {}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %v", results)
	}
}

func TestLoadIDEExecutions_MissingDir(t *testing.T) {
	t.Parallel()
	results, err := LoadIDEExecutions(context.Background(), "/nonexistent/path/xyz", map[string]struct{}{"x": {}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %v", results)
	}
}

func TestBuildIDEMergedRecords(t *testing.T) {
	t.Parallel()
	createdAt := time.UnixMilli(1000)
	endTime := time.UnixMilli(5000)
	sessions := []IDEParsedSession{{
		SessionID:          "sess-1",
		Title:              "My Session",
		WorkspaceDirectory: "/home/user/proj",
		CreatedAt:          createdAt,
	}}
	execResults := map[string]IDEExecutionResult{
		"sess-1": {TotalCredits: 0.25, StartTime: time.UnixMilli(2000), EndTime: endTime, Executions: 1},
	}

	records := BuildIDEMergedRecords(sessions, execResults)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	r := records[0]
	if r.SessionID != "sess-1" {
		t.Errorf("SessionID = %q", r.SessionID)
	}
	if r.Kind != RecordKindSessionMeta {
		t.Errorf("Kind = %q", r.Kind)
	}
	if r.Agent != "kiro-ide" {
		t.Errorf("Agent = %q", r.Agent)
	}
	if !approxEqual(r.TotalCredits, 0.25, 1e-9) {
		t.Errorf("TotalCredits = %v", r.TotalCredits)
	}
	if r.TotalInputTokens != 0 || r.TotalOutputTokens != 0 {
		t.Errorf("tokens should be 0")
	}
	if !r.UpdatedAt.Equal(endTime) {
		t.Errorf("UpdatedAt = %v, want %v", r.UpdatedAt, endTime)
	}
}

func TestBuildIDEMergedRecords_NoExecResult(t *testing.T) {
	t.Parallel()
	createdAt := time.UnixMilli(1000)
	sessions := []IDEParsedSession{{
		SessionID:          "sess-2",
		Title:              "Zero Credits",
		WorkspaceDirectory: "/home/user/proj",
		CreatedAt:          createdAt,
	}}

	records := BuildIDEMergedRecords(sessions, map[string]IDEExecutionResult{})
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	r := records[0]
	if r.Kind != RecordKindSessionMeta || r.Agent != "kiro-ide" {
		t.Errorf("Kind=%q Agent=%q", r.Kind, r.Agent)
	}
	if !approxEqual(r.TotalCredits, 0.0, 1e-9) {
		t.Errorf("TotalCredits = %v, want 0", r.TotalCredits)
	}
	if !r.UpdatedAt.Equal(createdAt) {
		t.Errorf("UpdatedAt = %v, want %v (createdAt fallback)", r.UpdatedAt, createdAt)
	}
}
