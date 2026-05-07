package monitor

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Force deterministic, ANSI-free rendering for snapshot tests.
// Must run in init() so it happens before any lipgloss style is created at
// package init time (styles in tui.go, tui_views_*.go are created at package init).
func init() {
	if err := os.Setenv("NO_COLOR", "1"); err != nil {
		panic(err)
	}
	if err := os.Setenv("TERM", "dumb"); err != nil {
		panic(err)
	}
	if err := os.Setenv("TZ", "UTC"); err != nil {
		panic(err)
	}
}

// update regenerates golden files when TestSnapshot* tests run. Usage:
//
//	go test -run TestSnapshot -update ./internal/monitor/...
var update = flag.Bool("update", false, "update golden files under testdata/snapshots/")

const snapshotDir = "testdata/snapshots"

// newSnapshotModel returns a *model suitable for snapshotting.
// Reuses newTestModel() from tui_test.go (same package) which already
// constructs a deterministic *model via NewModel(...) + fixture() + fixed
// width=140, height=40. Caller tweaks the `tab` (int) to select the view.
func newSnapshotModel(tab int) *model {
	m := newTestModel() // from tui_test.go — deterministic metrics + width/height
	m.tab = tab
	return m
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	// lipgloss v2 Style.Render always emits ANSI codes; NO_COLOR only affects
	// the colorprofile.Writer output path, not the string returned by Render.
	// Strip here to keep goldens readable and terminal-independent.
	got = stripANSI(got) // stripANSI defined in tui_test.go (same package)
	path := filepath.Join(snapshotDir, name+".golden")
	if *update {
		if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s: %v (run with -update to create)", path, err)
	}
	// Normalize CRLF → LF so golden files work on Windows where git may
	// check out files with \r\n line endings.
	wantStr := strings.ReplaceAll(string(want), "\r\n", "\n")
	if wantStr != got {
		t.Errorf("%s snapshot mismatch; rerun with -update to refresh\n--- want ---\n%s\n--- got ---\n%s",
			name, wantStr, got)
	}
}

func TestSnapshotRenderAgentsList(t *testing.T) {
	m := newSnapshotModel(tabAgents) // agents tab
	got := m.renderAgentsList()
	assertGolden(t, "agents_list", got)
}

func TestSnapshotRenderToolsList(t *testing.T) {
	m := newSnapshotModel(tabTools) // tools tab
	got := m.renderToolsList()
	assertGolden(t, "tools_list", got)
}

func TestSnapshotRenderSessionsList(t *testing.T) {
	m := newSnapshotModel(tabSessions) // sessions tab
	got := m.renderSessionsList()
	assertGolden(t, "sessions_list", got)
}

func TestSnapshotRenderRecentSessionsBox(t *testing.T) {
	m := newSnapshotModel(tabOverview)
	got := m.renderRecentSessionsBox(m.contentWidth())
	assertGolden(t, "overview_recent_sessions", got)
}

func TestSnapshotRenderSummaryBox(t *testing.T) {
	base := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	sessions := []SessionMetric{
		{ID: "aaa001", Agent: "coder", Cwd: "/proj/a", StartTime: base, EndTime: base.Add(10 * time.Minute), LastActivity: base.Add(10 * time.Minute), Duration: JSONDuration(10 * time.Minute), Active: true, ToolCalls: 8, Prompts: 3},
		{ID: "aaa002", Agent: "reviewer", Cwd: "/proj/b", StartTime: base.Add(5 * time.Minute), EndTime: base.Add(15 * time.Minute), LastActivity: base.Add(15 * time.Minute), Duration: JSONDuration(10 * time.Minute), Active: true, ToolCalls: 4, Prompts: 2},
		{ID: "aaa003", Agent: "coder", Cwd: "/proj/c", StartTime: base.Add(20 * time.Minute), EndTime: base.Add(30 * time.Minute), LastActivity: base.Add(30 * time.Minute), Duration: JSONDuration(10 * time.Minute), ToolCalls: 6, Prompts: 1},
		{ID: "aaa004", Agent: "tester", Cwd: "/proj/d", StartTime: base.Add(30 * time.Minute), EndTime: base.Add(45 * time.Minute), LastActivity: base.Add(45 * time.Minute), Duration: JSONDuration(15 * time.Minute), ToolCalls: 12, Prompts: 4},
		{ID: "aaa005", Agent: "explorer", Cwd: "/proj/e", StartTime: base.Add(60 * time.Minute), EndTime: base.Add(70 * time.Minute), LastActivity: base.Add(70 * time.Minute), Duration: JSONDuration(10 * time.Minute), ToolCalls: 3, Prompts: 1},
		{ID: "aaa006", Agent: "coder", Cwd: "/proj/f", StartTime: base.Add(90 * time.Minute), EndTime: base.Add(100 * time.Minute), LastActivity: base.Add(100 * time.Minute), Duration: JSONDuration(10 * time.Minute), ToolCalls: 5, Prompts: 2},
	}
	tools := []ToolMetric{
		{Name: "bash", CallCount: 15, ErrorCount: 2, ErrorRate: 0.13},
		{Name: "read_file", CallCount: 22, ErrorCount: 0},
		{Name: "write_file", CallCount: 18, ErrorCount: 1, ErrorRate: 0.06},
		{Name: "grep", CallCount: 10, ErrorCount: 3, ErrorRate: 0.30},
		{Name: "glob", CallCount: 8, ErrorCount: 0},
		{Name: "str_replace", CallCount: 14, ErrorCount: 0},
		{Name: "create_file", CallCount: 6, ErrorCount: 0},
		{Name: "delete_file", CallCount: 2, ErrorCount: 1, ErrorRate: 0.50},
		{Name: "list_dir", CallCount: 9, ErrorCount: 0},
		{Name: "run_tests", CallCount: 5, ErrorCount: 2, ErrorRate: 0.40},
		{Name: "git_status", CallCount: 4, ErrorCount: 0},
		{Name: "git_diff", CallCount: 3, ErrorCount: 0},
		{Name: "git_commit", CallCount: 2, ErrorCount: 0},
		{Name: "search_symbols", CallCount: 7, ErrorCount: 0},
		{Name: "goto_definition", CallCount: 6, ErrorCount: 0},
		{Name: "find_references", CallCount: 5, ErrorCount: 0},
		{Name: "get_hover", CallCount: 4, ErrorCount: 0},
		{Name: "get_diagnostics", CallCount: 3, ErrorCount: 1, ErrorRate: 0.33},
		{Name: "pattern_search", CallCount: 2, ErrorCount: 0},
		{Name: "rename_symbol", CallCount: 1, ErrorCount: 0},
		{Name: "format", CallCount: 3, ErrorCount: 0},
		{Name: "web_search", CallCount: 2, ErrorCount: 0},
		{Name: "fetch_url", CallCount: 1, ErrorCount: 0},
	}
	agents := []AgentMetric{
		{Name: "coder", SessionCount: 3, ToolCalls: 19, Prompts: 6},
		{Name: "reviewer", SessionCount: 1, ToolCalls: 4, Prompts: 2},
		{Name: "tester", SessionCount: 1, ToolCalls: 12, Prompts: 4},
		{Name: "explorer", SessionCount: 1, ToolCalls: 3, Prompts: 1},
	}
	m := newSnapshotModel(tabOverview)
	m.metrics.Overview.Sessions = sessions
	m.metrics.Overview.Tools = tools
	m.metrics.Overview.Agents = agents
	m.kiroUsage = nil
	m.kiroUsageRead = nil
	m.recomputeSummaryTotals()
	got := m.renderSummaryBox(m.contentWidth())
	assertGolden(t, "summary_box", got)
}
