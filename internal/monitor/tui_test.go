package monitor

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

// fixture builds a small but realistic DetailedMetrics value.
func fixture() DetailedMetrics {
	start := time.Date(2026, 4, 20, 9, 0, 0, 0, time.UTC)
	timeline1 := []EventEntry{
		{Ts: start, Event: apmconfig.EventAgentSpawn},
		{Ts: start.Add(1 * time.Second), Event: apmconfig.EventUserPromptSubmit},
		{Ts: start.Add(2 * time.Second), Event: apmconfig.EventPreToolUse, Tool: "bash"},
		{Ts: start.Add(3 * time.Second), Event: apmconfig.EventPostToolUse, Tool: "bash"},
		{Ts: start.Add(4 * time.Second), Event: apmconfig.EventPreToolUse, Tool: "grep", IsError: true},
		{Ts: start.Add(5 * time.Second), Event: apmconfig.EventStop},
	}
	s1 := SessionDetail{
		SessionMetric: SessionMetric{
			ID: "abcdef123456789012", Agent: "coder", Cwd: "/tmp",
			StartTime: start, EndTime: start.Add(5 * time.Second), LastActivity: start.Add(5 * time.Second),
			Duration: JSONDuration(5 * time.Second), Active: true, ToolCalls: 2, Prompts: 1,
		},
		PromptHistory: []string{"implement\nfeature x"},
		Timeline:      timeline1,
	}
	s2 := SessionDetail{
		SessionMetric: SessionMetric{
			ID: "fedcba987654321098", Agent: "explorer", Cwd: "/tmp",
			StartTime: start.Add(time.Hour), EndTime: start.Add(time.Hour + time.Minute),
			LastActivity: start.Add(time.Hour + time.Minute),
			Duration:     JSONDuration(time.Minute), Prompts: 0, ToolCalls: 0,
		},
	}
	return DetailedMetrics{
		Overview: Metrics{
			Sessions: []SessionMetric{s1.SessionMetric, s2.SessionMetric},
			Tools: []ToolMetric{
				{Name: "bash", CallCount: 1, ErrorCount: 0},
				{Name: "grep", CallCount: 1, ErrorCount: 1, ErrorRate: 1},
			},
			Agents: []AgentMetric{
				{Name: "coder", SessionCount: 1, ToolCalls: 2, Prompts: 1},
				{Name: "explorer", SessionCount: 1},
			},
			HourlyActivity: []HourlyMetric{
				{Hour: start, EventCount: 6},
			},
		},
		Sessions: []SessionDetail{s1, s2},
		Agents: []AgentDetail{
			{
				AgentMetric:  AgentMetric{Name: "coder", SessionCount: 1, ToolCalls: 2, Prompts: 1},
				Sessions:     []SessionMetric{s1.SessionMetric},
				ToolSummary:  []SessionToolSummary{{Tool: "bash", CallCount: 1, ErrorCount: 1}, {Tool: "grep", CallCount: 1}},
				ToolErrorCnt: 1,
			},
			{
				AgentMetric: AgentMetric{Name: "explorer", SessionCount: 1},
				Sessions:    []SessionMetric{s2.SessionMetric},
			},
		},
		Tools: []ToolDetail{
			{
				ToolMetric:  ToolMetric{Name: "bash", CallCount: 1},
				AvgDuration: JSONDuration(time.Second),
				RecentCalls: []ToolCall{{Ts: start.Add(2 * time.Second), Session: "abcdef123456789012", Agent: "coder", Tool: "bash", Duration: JSONDuration(time.Second), InputSummary: "echo hi"}},
			},
			{
				ToolMetric: ToolMetric{Name: "grep", CallCount: 1, ErrorCount: 1, ErrorRate: 1},
				Errors:     []ToolCall{{Ts: start.Add(4 * time.Second), Session: "abcdef123456789012", Agent: "coder", Tool: "grep", IsError: true, InputSummary: "find foo in bar"}},
			},
		},
		Skills: []SkillUsage{
			{Name: "task-verification", ReadCount: 3},
			{Name: "git-master", ReadCount: 1},
		},
	}
}

func newTestModel() *model {
	m := NewModel(".kiro/logs", 24*time.Hour)
	m.metrics = fixture()
	m.width = 140
	m.height = 40
	return m
}

func press(m *model, key string) *model {
	next, _ := m.handleKey(key)
	return next.(*model)
}

func TestTUIOverviewRender(t *testing.T) {
	m := newTestModel()
	out := m.renderView()
	for _, want := range []string{"kapm monitor", "Overview", "Sessions", "Agents", "Tools", "Summary", "Top tools", "Top agents", "Activity"} {
		if !strings.Contains(out, want) {
			t.Errorf("overview missing %q", want)
		}
	}
}

func TestTUITabsSwitchViaTab(t *testing.T) {
	m := newTestModel()
	m = press(m, "tab")
	if m.tab != tabSessions {
		t.Fatalf("tab: want %d, got %d", tabSessions, m.tab)
	}
	out := m.renderView()
	// 12-char prefix visible
	if !strings.Contains(out, "abcdef123456") {
		t.Errorf("sessions tab missing 12-char session ID prefix, output:\n%s", out)
	}
}

func TestTUISessionDetail(t *testing.T) {
	m := newTestModel()
	m = press(m, "2") // Sessions tab
	m = press(m, "enter")
	if !m.detail {
		t.Fatal("expected detail=true after enter")
	}
	out := m.renderView()
	if !strings.Contains(out, "abcdef123456789012") {
		t.Errorf("detail missing full session ID")
	}
	if !strings.Contains(out, "implement feature x") {
		t.Errorf("detail missing single-line prompt")
	}
	if !strings.Contains(out, "bash") {
		t.Errorf("detail missing timeline event")
	}
}

func TestTUIAgentDetail(t *testing.T) {
	m := newTestModel()
	m = press(m, "3") // Agents
	m = press(m, "enter")
	out := m.renderView()
	if !strings.Contains(out, "coder") {
		t.Errorf("agent detail missing agent name")
	}
	if !strings.Contains(out, "Top tools used") {
		t.Errorf("agent detail missing top-tools section")
	}
}

func TestTUIToolDetail(t *testing.T) {
	m := newTestModel()
	m = press(m, "4") // Tools
	m = press(m, "enter")
	out := m.renderView()
	if !strings.Contains(out, "Recent calls") || !strings.Contains(out, "Error samples") {
		t.Errorf("tool detail missing sections")
	}
}

func TestTUIBackFromDetail(t *testing.T) {
	m := newTestModel()
	m.tab = tabSessions
	m.detail = true
	m = press(m, "esc")
	if m.detail {
		t.Errorf("expected detail=false after esc")
	}
}

func TestTUIDownUpSelection(t *testing.T) {
	m := newTestModel()
	m = press(m, "2") // Sessions
	m = press(m, "down")
	if m.cursor[tabSessions] != 1 {
		t.Errorf("expected cursor=1, got %d", m.cursor[tabSessions])
	}
	m = press(m, "up")
	if m.cursor[tabSessions] != 0 {
		t.Errorf("expected cursor=0, got %d", m.cursor[tabSessions])
	}
}

func TestTUIRenderEmpty(t *testing.T) {
	m := NewModel("/nowhere", time.Hour)
	m.width, m.height = 100, 30
	out := m.renderView()
	if !strings.Contains(out, "No log data found") {
		t.Errorf("expected empty-state message, got: %s", out)
	}
}

func TestTUIHelpLineChanges(t *testing.T) {
	m := newTestModel()
	if !strings.Contains(m.helpLine(), "switch") {
		t.Errorf("overview help missing tab hint")
	}
	m.tab = tabSessions
	if !strings.Contains(m.helpLine(), "select") || !strings.Contains(m.helpLine(), "open") {
		t.Errorf("list help missing select/open hint")
	}
	m.detail = true
	if !strings.Contains(m.helpLine(), "back") {
		t.Errorf("detail help missing back hint")
	}
}

func TestTUITabFiveSkills(t *testing.T) {
	m := newTestModel()
	m = press(m, "5")
	if m.tab != tabSkills {
		t.Fatalf("tab: want %d, got %d", tabSkills, m.tab)
	}
	out := m.renderView()
	for _, want := range []string{"5 Skills", "task-verification", "git-master"} {
		if !strings.Contains(out, want) {
			t.Errorf("skills tab output missing %q", want)
		}
	}
}

func TestTUIOverviewHasSkillsBox(t *testing.T) {
	m := newTestModel()
	out := m.renderView()
	if !strings.Contains(out, "Skills (reads)") {
		t.Errorf("overview missing Skills box")
	}
	if !strings.Contains(out, "task-verification") {
		t.Errorf("overview skills box missing entry")
	}
}

func TestTUIOverviewHidesSkillsBoxWhenEmpty(t *testing.T) {
	m := newTestModel()
	m.metrics.Skills = nil
	out := m.renderView()
	if strings.Contains(out, "Skills (reads)") {
		t.Errorf("overview should hide Skills box when no skills")
	}
}

func TestTUISessionsListHasLastActivityColumn(t *testing.T) {
	m := newTestModel()
	m.tab = tabSessions
	out := m.renderView()
	if !strings.Contains(out, "Last act") {
		t.Errorf("sessions list missing Last activity column header")
	}
}

func TestTUIToolDetailErrorHasInput(t *testing.T) {
	m := newTestModel()
	m = press(m, "4") // Tools
	// cursor defaults to 0 → bash (no errors), advance to grep
	m = press(m, "down")
	m = press(m, "enter")
	out := m.renderView()
	if !strings.Contains(out, "Input") {
		t.Errorf("tool detail error samples missing Input column header")
	}
	if !strings.Contains(out, "find foo in bar") {
		t.Errorf("tool detail error samples missing InputSummary text")
	}
}

// largeSkillsFixture builds metrics with 12 skills to exercise the top-10 cap.
func largeSkillsFixture() DetailedMetrics {
	m := fixture()
	skills := make([]SkillUsage, 0, 12)
	for i := 0; i < 12; i++ {
		skills = append(skills, SkillUsage{
			Name:      "skill-" + string(rune('a'+i)),
			ReadCount: 12 - i,
		})
	}
	m.Skills = skills
	return m
}

func TestTUIOverviewSkillsCapTen(t *testing.T) {
	m := newTestModel()
	m.metrics = largeSkillsFixture()
	out := m.renderView()
	// First 10 names should render; 11th and 12th must be excluded.
	for i := 0; i < 10; i++ {
		name := "skill-" + string(rune('a'+i))
		if !strings.Contains(out, name) {
			t.Errorf("overview skills missing %q (should show top 10)", name)
		}
	}
	for _, name := range []string{"skill-k", "skill-l"} {
		if strings.Contains(out, name) {
			t.Errorf("overview skills should not include %q (beyond top 10)", name)
		}
	}
}

func TestTUIRecentSessionsCapTen(t *testing.T) {
	m := newTestModel()
	// Build 12 sessions to verify the box shows 10 in Recent active sessions.
	base := fixture()
	now := time.Now()
	base.Sessions = nil
	for i := 0; i < 12; i++ {
		// Distinct 12-char IDs: sess00000000, sess00000001, … sess00000011
		id := fmt.Sprintf("sess%08d", i)
		base.Sessions = append(base.Sessions, SessionDetail{
			SessionMetric: SessionMetric{
				ID: id, Agent: "a", LastActivity: now.Add(-time.Duration(i) * time.Minute),
			},
		})
	}
	m.metrics = base
	recent := m.renderRecentSessionsBox(m.contentWidth())
	// Each session row contains its unique 12-char ID "sessNNNNNNNN".
	// Expect IDs 0..9 present; 10 and 11 absent (top 10).
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("sess%08d", i)
		if !strings.Contains(recent, id) {
			t.Errorf("recent sessions missing %q", id)
		}
	}
	for i := 10; i < 12; i++ {
		id := fmt.Sprintf("sess%08d", i)
		if strings.Contains(recent, id) {
			t.Errorf("recent sessions should not include %q (beyond top 10)", id)
		}
	}
}

func TestTUIDetailNavRight(t *testing.T) {
	m := newTestModel()
	m = press(m, "2")     // sessions tab (2 sessions)
	m = press(m, "enter") // enter detail on first session (cursor=0)
	if m.cursor[tabSessions] != 0 {
		t.Fatalf("expected cursor=0 on enter, got %d", m.cursor[tabSessions])
	}
	m = press(m, "right")
	if m.cursor[tabSessions] != 1 {
		t.Errorf("after right: expected cursor=1, got %d", m.cursor[tabSessions])
	}
	if m.detailScroll != 0 {
		t.Errorf("after right: expected detailScroll=0, got %d", m.detailScroll)
	}
	// rendered view should show second session's ID (index 1 in sorted order)
	out := m.renderView()
	sessions := m.metrics.Sessions
	if !strings.Contains(out, sessions[1].ID) {
		t.Errorf("after right: expected session ID %q in view", sessions[1].ID)
	}
}

func TestTUIDetailNavClamp(t *testing.T) {
	m := newTestModel()
	m = press(m, "2")
	m = press(m, "enter")
	m = press(m, "left") // already at 0
	if m.cursor[tabSessions] != 0 {
		t.Errorf("left at 0: expected cursor=0, got %d", m.cursor[tabSessions])
	}
	m = press(m, "right") // now at 1 (last item with 2 sessions)
	if m.cursor[tabSessions] != 1 {
		t.Errorf("after right: expected cursor=1, got %d", m.cursor[tabSessions])
	}
	m = press(m, "right") // should clamp at 1
	if m.cursor[tabSessions] != 1 {
		t.Errorf("right at end: expected cursor=1, got %d", m.cursor[tabSessions])
	}
}

func TestTUIDetailNavResetsScroll(t *testing.T) {
	m := newTestModel()
	m.height = 15
	m = press(m, "2")
	m = press(m, "enter")
	m = press(m, "down") // scroll to 1
	if m.detailScroll != 1 {
		t.Fatalf("expected detailScroll=1 after down, got %d", m.detailScroll)
	}
	m = press(m, "right") // switch item
	if m.detailScroll != 0 {
		t.Errorf("after right: expected detailScroll=0, got %d", m.detailScroll)
	}
}

func TestTUISessionDetailNoCap(t *testing.T) {
	m := newTestModel()
	// Build a session with >30 timeline events.
	base := fixture()
	start := time.Date(2026, 4, 20, 9, 0, 0, 0, time.UTC)
	tl := make([]EventEntry, 35)
	for i := range tl {
		tl[i] = EventEntry{
			Ts:    start.Add(time.Duration(i) * time.Second),
			Event: apmconfig.EventPreToolUse,
			Tool:  fmt.Sprintf("tool%02d", i),
		}
	}
	base.Sessions[0].Timeline = tl
	m.metrics = base
	m = press(m, "2")
	m = press(m, "enter")
	// Scroll to bottom to ensure all events are accessible.
	m = press(m, "end")
	out := m.renderView()
	// Last tool name should appear somewhere in the rendered output.
	if !strings.Contains(out, "tool34") {
		t.Errorf("expected tool34 in rendered output (no 30-cap), got:\n%s", out)
	}
}

func TestTUISessionDetailShowsInputSummary(t *testing.T) {
	m := newTestModel()
	base := fixture()
	base.Sessions[0].Timeline[2].InputSummary = "echo hello"
	m.metrics = base
	m = press(m, "2")
	m = press(m, "enter")
	out := m.renderView()
	if !strings.Contains(out, "echo hello") {
		t.Errorf("expected InputSummary 'echo hello' in rendered output")
	}
}

func TestTUISessionDetailShowsDuration(t *testing.T) {
	m := newTestModel()
	base := fixture()
	base.Sessions[0].Timeline[2].Duration = JSONDuration(1200 * time.Millisecond)
	m.metrics = base
	m = press(m, "2")
	m = press(m, "enter")
	out := m.renderView()
	if !strings.Contains(out, "[1s]") {
		t.Errorf("expected '[1s]' in rendered output for 1200ms duration")
	}
}

func TestTUITabKeyDoesNotNavInDetail(t *testing.T) {
	m := newTestModel()
	m = press(m, "2")
	m = press(m, "enter")
	tab := m.tab
	cursor := m.cursor[m.tab]
	m = press(m, "tab")
	if m.tab != tab {
		t.Errorf("tab in detail: expected tab=%d, got %d", tab, m.tab)
	}
	if m.cursor[m.tab] != cursor {
		t.Errorf("tab in detail: expected cursor=%d, got %d", cursor, m.cursor[m.tab])
	}
}

func TestTUIDetailScrollKeys(t *testing.T) {
	m := newTestModel()
	m.height = 15         // small viewport so detail overflows
	m = press(m, "2")     // sessions tab
	m = press(m, "enter") // enter detail
	if !m.detail {
		t.Fatal("expected detail mode")
	}
	if m.detailScroll != 0 {
		t.Fatalf("expected detailScroll=0 on enter, got %d", m.detailScroll)
	}
	// Down moves scroll by 1
	m = press(m, "down")
	if m.detailScroll != 1 {
		t.Errorf("after down: expected detailScroll=1, got %d", m.detailScroll)
	}
	m = press(m, "j")
	if m.detailScroll != 2 {
		t.Errorf("after j: expected detailScroll=2, got %d", m.detailScroll)
	}
	// Up moves back
	m = press(m, "up")
	if m.detailScroll != 1 {
		t.Errorf("after up: expected detailScroll=1, got %d", m.detailScroll)
	}
	// home resets
	m = press(m, "pgdown")
	if m.detailScroll == 1 {
		t.Errorf("pgdown should have moved scroll")
	}
	m = press(m, "home")
	if m.detailScroll != 0 {
		t.Errorf("after home: expected detailScroll=0, got %d", m.detailScroll)
	}
	// end jumps to max
	m = press(m, "end")
	if m.detailScroll == 0 {
		t.Errorf("after end: expected detailScroll>0")
	}
	// esc exits and resets scroll
	m = press(m, "esc")
	if m.detail {
		t.Errorf("expected detail=false after esc")
	}
	if m.detailScroll != 0 {
		t.Errorf("expected detailScroll=0 after esc, got %d", m.detailScroll)
	}
}

func TestTUIListNavUnchangedOutsideDetail(t *testing.T) {
	// up/down in list mode still move cursor, not scroll.
	m := newTestModel()
	m = press(m, "2") // sessions list
	if m.detail {
		t.Fatal("should not be in detail")
	}
	m = press(m, "down")
	if m.cursor[tabSessions] != 1 {
		t.Errorf("expected cursor=1 in list mode, got %d", m.cursor[tabSessions])
	}
	if m.detailScroll != 0 {
		t.Errorf("detailScroll should be 0 in list mode, got %d", m.detailScroll)
	}
}

func TestTUIDetailHelpLine(t *testing.T) {
	m := newTestModel()
	m.tab = tabSessions
	m.detail = true
	h := m.helpLine()
	for _, want := range []string{"prev/next", "scroll", "pgup/pgdn", "back"} {
		if !strings.Contains(h, want) {
			t.Errorf("detail help missing %q (got %q)", want, h)
		}
	}
}

func TestTUITimelineHidesPostToolUse(t *testing.T) {
	m := newTestModel()
	m = press(m, "2")
	m = press(m, "enter")
	out := m.renderView()
	// Non-error postToolUse should be hidden.
	if strings.Contains(out, "postToolUse") {
		t.Errorf("non-error postToolUse should be hidden from timeline")
	}
	// Error preToolUse (grep, IsError=true) should still appear.
	if !strings.Contains(out, "grep") {
		t.Errorf("error preToolUse should still appear")
	}
}

func TestTUITimelineSimplifiedLabels(t *testing.T) {
	m := newTestModel()
	m = press(m, "2")
	m = press(m, "enter")
	out := m.renderView()
	// Should show simplified labels, not raw event names.
	if strings.Contains(out, "agentSpawn") {
		t.Errorf("should show 'spawn' not 'agentSpawn'")
	}
	if strings.Contains(out, "userPromptSubmit") {
		t.Errorf("should show 'prompt' not 'userPromptSubmit'")
	}
	if !strings.Contains(out, "spawn") {
		t.Errorf("expected 'spawn' label in timeline")
	}
	if !strings.Contains(out, "prompt") {
		t.Errorf("expected 'prompt' label in timeline")
	}
}

func TestTUITimelineTimeOnly(t *testing.T) {
	m := newTestModel()
	m = press(m, "2")
	m = press(m, "enter")
	out := m.renderView()
	// Timeline section should use time-only format (HH:MM:SS).
	// The header section still uses full timestamps, so check that
	// "spawn" line (a timeline entry) does NOT have the date prefix.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "spawn") && strings.Contains(line, "2026-04-20") {
			t.Errorf("timeline should show time-only, not full date: %s", line)
		}
	}
}

func TestTUITimelinePathShortening(t *testing.T) {
	m := newTestModel()
	base := fixture()
	base.Sessions[0].Cwd = "/home/user/project"
	base.Sessions[0].Timeline[2].InputSummary = "/home/user/project/src/main.go"
	m.metrics = base
	m = press(m, "2")
	m = press(m, "enter")
	out := m.renderView()
	// Should show project-relative path.
	if !strings.Contains(out, "src/main.go") {
		t.Errorf("expected project-relative path 'src/main.go' in timeline")
	}
}

func TestSwitchToTab(t *testing.T) {
	m := NewModel(".", time.Hour)
	m.detail = true
	m.detailScroll = 42
	m.tab = tabOverview

	m.switchToTab(tabAgents)

	if m.tab != tabAgents {
		t.Errorf("tab: got %d, want %d", m.tab, tabAgents)
	}
	if m.detail {
		t.Error("detail should be false")
	}
	if m.detailScroll != 0 {
		t.Errorf("detailScroll: got %d, want 0", m.detailScroll)
	}
}

func TestSingleLine(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello world", "hello world"},
		{"hello\nworld", "hello world"},
		{"hello\tworld", "hello world"},
		{"hello\rworld", "hello world"},
		{"hello\n\nworld", "hello world"},
		{"hello\t\tworld", "hello world"},
		{"  hello   world  ", "hello world"},
		{"hello\nworld\ttab\rcarriage", "hello world tab carriage"},
	}
	for _, tt := range tests {
		got := singleLine(tt.input)
		if got != tt.expected {
			t.Errorf("singleLine(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestTUIDetailUnrecognizedKeyDoesNotAffectListCursor(t *testing.T) {
	m := newTestModel()
	m = press(m, "2")     // sessions tab
	m = press(m, "down")  // cursor = 1
	m = press(m, "enter") // enter detail
	if !m.detail {
		t.Fatal("expected detail mode")
	}
	cursorBefore := m.cursor[tabSessions]
	// Press an unrecognized key in detail mode.
	m = press(m, "x")
	if m.cursor[tabSessions] != cursorBefore {
		t.Errorf("unrecognized key in detail mode changed cursor: want %d, got %d", cursorBefore, m.cursor[tabSessions])
	}
	if !m.detail {
		t.Errorf("unrecognized key in detail mode should not exit detail")
	}
}

// TestAbbrevHome_SinglyResolved verifies that os.UserHomeDir is called once at
// model construction and that abbrevHome uses the cached value, not a per-call
// syscall. We prove this by setting m.homeDir directly and confirming the
// function uses it without re-reading the environment.
func TestAbbrevHome_SinglyResolved(t *testing.T) {
	fakeHome := "/tmp/fakehome"
	// abbrevHome uses the passed-in home, not os.UserHomeDir.
	if got := abbrevHome(fakeHome, fakeHome+"/foo"); got != "~/foo" {
		t.Errorf("abbrevHome(%q, %q) = %q, want %q", fakeHome, fakeHome+"/foo", got, "~/foo")
	}
	if got := abbrevHome(fakeHome, fakeHome); got != "~" {
		t.Errorf("abbrevHome(%q, %q) = %q, want %q", fakeHome, fakeHome, got, "~")
	}
	if got := abbrevHome(fakeHome, "/other/path"); got != "/other/path" {
		t.Errorf("abbrevHome(%q, %q) = %q, want %q", fakeHome, "/other/path", got, "/other/path")
	}
	if got := abbrevHome("", "/some/path"); got != "/some/path" {
		t.Errorf("abbrevHome(%q, %q) = %q, want %q", "", "/some/path", got, "/some/path")
	}

	// Verify model stores homeDir once at construction.
	t.Setenv("HOME", fakeHome)
	m := NewModel("/tmp/logs", 24*time.Hour)
	if m.homeDir != fakeHome {
		t.Errorf("NewModel homeDir = %q, want %q", m.homeDir, fakeHome)
	}
	// Render 100 times — homeDir must remain the value set at construction.
	for i := 0; i < 100; i++ {
		if m.homeDir != fakeHome {
			t.Fatalf("homeDir changed after render %d", i)
		}
		_ = m.renderView()
	}
}

func TestTUISessionsListGroupingIndent(t *testing.T) {
	m := newTestModel()
	base := fixture()
	sid := "shared-session-id"
	base.Sessions = []SessionDetail{
		{SessionMetric: SessionMetric{ID: sid, Agent: "orchestrator", LastActivity: time.Now()}},
		{SessionMetric: SessionMetric{ID: sid, Agent: "lead", LastActivity: time.Now().Add(-time.Second)}},
	}
	m.metrics = base
	m.tab = tabSessions
	out := m.renderSessionsList()

	lines := strings.Split(out, "\n")
	var orchLine, leadLine string
	for _, l := range lines {
		if strings.Contains(l, "orchestrator") {
			orchLine = l
		}
		if strings.Contains(l, "lead") && !strings.Contains(l, "orchestrator") {
			leadLine = l
		}
	}
	if orchLine == "" {
		t.Fatalf("orchestrator row not found\noutput:\n%s", out)
	}
	if leadLine == "" {
		t.Fatalf("lead row not found\noutput:\n%s", out)
	}
	// First row: ID cell should contain the shared sid prefix.
	if !strings.Contains(orchLine, "shared-sessi") {
		t.Errorf("first row should show ID prefix, got: %q", orchLine)
	}
	// Second row: ID cell should be blank — the sid prefix must NOT appear.
	if strings.Contains(leadLine, "shared-sessi") {
		t.Errorf("second row should not show ID, got: %q", leadLine)
	}
	// Second row: agent cell should be indented with 2 spaces before "lead".
	if !strings.Contains(leadLine, "  lead") {
		t.Errorf("second row agent should be indented with 2 spaces, got: %q", leadLine)
	}
}

func TestTUIRenderSessionDetailTitle(t *testing.T) {
	m := newTestModel()
	base := fixture()
	base.Sessions[0].Title = "my-test-title"
	m.metrics = base
	m = press(m, "2")
	m = press(m, "enter")
	out := m.renderView()
	if !strings.Contains(out, "title:") {
		t.Errorf("session detail missing 'title:' label")
	}
	if !strings.Contains(out, "my-test-title") {
		t.Errorf("session detail missing title text")
	}
}

func TestTUIRecentSessionsBoxGroupingIndent(t *testing.T) {
	m := newTestModel()
	base := fixture()
	sid := "shared-session-id"
	base.Sessions = []SessionDetail{
		{SessionMetric: SessionMetric{ID: sid, Agent: "orchestrator", LastActivity: time.Now()}},
		{SessionMetric: SessionMetric{ID: sid, Agent: "lead", LastActivity: time.Now().Add(-time.Second)}},
	}
	m.metrics = base
	out := m.renderRecentSessionsBox(m.contentWidth())

	lines := strings.Split(out, "\n")
	var orchLine, leadLine string
	for _, l := range lines {
		if strings.Contains(l, "orchestrator") {
			orchLine = l
		}
		if strings.Contains(l, "lead") && !strings.Contains(l, "orchestrator") {
			leadLine = l
		}
	}
	if orchLine == "" {
		t.Fatalf("orchestrator row not found\noutput:\n%s", out)
	}
	if leadLine == "" {
		t.Fatalf("lead row not found\noutput:\n%s", out)
	}
	if !strings.Contains(orchLine, "shared-sessi") {
		t.Errorf("first row should show ID prefix, got: %q", orchLine)
	}
	if strings.Contains(leadLine, "shared-sessi") {
		t.Errorf("second row should not show ID, got: %q", leadLine)
	}
	if !strings.Contains(leadLine, "  lead") {
		t.Errorf("second row agent should be indented with 2 spaces, got: %q", leadLine)
	}
}

func TestTUIAgentDetailTitleColumn(t *testing.T) {
	m := newTestModel()
	m = press(m, "3") // Agents tab
	m = press(m, "enter")
	out := m.renderView()
	if !strings.Contains(out, "Title") {
		t.Errorf("agent detail sessions table missing 'Title' column header")
	}
}

func TestTUISessionsListTitleTruncation(t *testing.T) {
	m := newTestModel()
	base := fixture()
	longTitle := "こんにちは、これはサンプルプロンプトです。このタイトルは非常に長いです。"
	base.Sessions = []SessionDetail{
		{SessionMetric: SessionMetric{ID: "abc123", Agent: "coder", Title: longTitle, LastActivity: time.Now()}},
	}
	m.metrics = base
	m.tab = tabSessions
	out := m.renderSessionsList()

	if !strings.Contains(out, "…") {
		t.Errorf("expected truncation marker '…' in output for long title")
	}
	// Full title should not appear verbatim.
	if strings.Contains(out, longTitle) {
		t.Errorf("full long title should be truncated, but appeared verbatim")
	}
}
