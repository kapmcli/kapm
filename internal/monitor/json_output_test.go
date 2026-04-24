package monitor_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/monitor"
)

const testLogsDir = "../../testdata/monitor"
const testSince = 8760 * time.Hour // 1 year

func TestRunJSON_NoFilter(t *testing.T) {
	var buf bytes.Buffer
	if err := monitor.RunJSON(testLogsDir, testSince, "", "", &buf); err != nil {
		t.Fatalf("RunJSON: %v", err)
	}
	var dm monitor.DetailedMetrics
	if err := json.Unmarshal(buf.Bytes(), &dm); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(dm.Sessions) == 0 {
		t.Error("expected sessions, got none")
	}
}

// TestRunJSON_SessionFilter: --session alone returns merged view (Agent=="(all)").
func TestRunJSON_SessionFilter(t *testing.T) {
	var buf bytes.Buffer
	if err := monitor.RunJSON(testLogsDir, testSince, "sess-1", "", &buf); err != nil {
		t.Fatalf("RunJSON: %v", err)
	}
	var dm monitor.DetailedMetrics
	if err := json.Unmarshal(buf.Bytes(), &dm); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(dm.Sessions) != 1 {
		t.Fatalf("expected 1 merged session, got %d", len(dm.Sessions))
	}
	s := dm.Sessions[0]
	if s.ID != "sess-1" {
		t.Errorf("expected ID=sess-1, got %q", s.ID)
	}
	if s.Agent != "(all)" {
		t.Errorf("expected Agent=(all), got %q", s.Agent)
	}
}

func TestRunJSON_AgentFilter(t *testing.T) {
	var buf bytes.Buffer
	if err := monitor.RunJSON(testLogsDir, testSince, "", "kiro", &buf); err != nil {
		t.Fatalf("RunJSON: %v", err)
	}
	var dm monitor.DetailedMetrics
	if err := json.Unmarshal(buf.Bytes(), &dm); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, s := range dm.Sessions {
		if s.Agent != "kiro" {
			t.Errorf("unexpected agent %q in filtered sessions", s.Agent)
		}
	}
	for _, a := range dm.Agents {
		if a.Name != "kiro" {
			t.Errorf("unexpected agent %q in filtered agents", a.Name)
		}
	}
}

// TestRunJSON_BothFilters: --session + --agent returns per-agent entry.
func TestRunJSON_BothFilters(t *testing.T) {
	var buf bytes.Buffer
	if err := monitor.RunJSON(testLogsDir, testSince, "sess-1", "kiro", &buf); err != nil {
		t.Fatalf("RunJSON: %v", err)
	}
	var dm monitor.DetailedMetrics
	if err := json.Unmarshal(buf.Bytes(), &dm); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(dm.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(dm.Sessions))
	}
	s := dm.Sessions[0]
	if s.ID != "sess-1" || s.Agent != "kiro" {
		t.Errorf("unexpected session id=%q agent=%q", s.ID, s.Agent)
	}
}

// TestRunJSON_NoMatchSession: --session with missing sid exits with error containing sid.
func TestRunJSON_NoMatchSession(t *testing.T) {
	var buf bytes.Buffer
	err := monitor.RunJSON(testLogsDir, testSince, "nonexistent-sid", "", &buf)
	if err == nil {
		t.Fatal("expected error for missing session, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent-sid") {
		t.Errorf("error %q does not contain sid", err.Error())
	}
}

// TestRunJSON_NoMatchSessionAndAgent: --session + --agent with missing agent exits with error containing both.
func TestRunJSON_NoMatchSessionAndAgent(t *testing.T) {
	var buf bytes.Buffer
	err := monitor.RunJSON(testLogsDir, testSince, "sess-1", "nonexistent-agent", &buf)
	if err == nil {
		t.Fatal("expected error for missing session+agent, got nil")
	}
	if !strings.Contains(err.Error(), "sess-1") {
		t.Errorf("error %q does not contain sid", err.Error())
	}
	if !strings.Contains(err.Error(), "nonexistent-agent") {
		t.Errorf("error %q does not contain agent name", err.Error())
	}
}
