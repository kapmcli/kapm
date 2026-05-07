package monitor

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
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
	if string(want) != got {
		t.Errorf("%s snapshot mismatch; rerun with -update to refresh\n--- want ---\n%s\n--- got ---\n%s",
			name, string(want), got)
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
