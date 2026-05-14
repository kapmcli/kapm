package monitor

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"github.com/kapmcli/kapm/internal/kirocliusage"
)

func TestOverviewLayout(t *testing.T) {
	cases := []struct {
		name   string
		width  int
		height int
		want   overviewParams
	}{
		// Full-size terminal
		{"full size 120x50", 120, 50, overviewParams{topN: 10, recentN: 10, showActivity: true, showRow2: true, columns: 3}},
		// Height boundary: avail=44 (height=50)
		{"avail=44 boundary", 120, 50, overviewParams{topN: 10, recentN: 10, showActivity: true, showRow2: true, columns: 3}},
		// Height boundary: avail=43 (height=49)
		{"avail=43 boundary", 120, 49, overviewParams{topN: 5, recentN: 5, showActivity: true, showRow2: true, columns: 3}},
		// Height boundary: avail=34 (height=40)
		{"avail=34 boundary", 120, 40, overviewParams{topN: 5, recentN: 5, showActivity: true, showRow2: true, columns: 3}},
		// Height boundary: avail=33 (height=39)
		{"avail=33 boundary", 120, 39, overviewParams{topN: 5, recentN: 3, showActivity: false, showRow2: true, columns: 3}},
		// Height boundary: avail=24 (height=30)
		{"avail=24 boundary", 120, 30, overviewParams{topN: 5, recentN: 3, showActivity: false, showRow2: true, columns: 3}},
		// Height boundary: avail=23 (height=29) → showRow2=false
		{"avail=23 boundary", 120, 29, overviewParams{topN: 5, recentN: 3, showActivity: false, showRow2: false, columns: 3}},
		// 80x24 terminal: avail=18, width=80
		{"80x24 terminal", 80, 24, overviewParams{topN: 5, recentN: 3, showActivity: false, showRow2: false, columns: 2}},
		// Width boundary: width=100
		{"width=100 boundary", 100, 50, overviewParams{topN: 10, recentN: 10, showActivity: true, showRow2: true, columns: 3}},
		// Width boundary: width=99
		{"width=99 boundary", 99, 50, overviewParams{topN: 10, recentN: 10, showActivity: true, showRow2: true, columns: 2}},
		// Width boundary: width=80
		{"width=80 boundary", 80, 50, overviewParams{topN: 10, recentN: 10, showActivity: true, showRow2: true, columns: 2}},
		// Width boundary: width=79 → narrow rule caps topN/recentN to 3
		{"width=79 boundary", 79, 50, overviewParams{topN: 3, recentN: 3, showActivity: true, showRow2: true, columns: 1}},
		// Narrow terminal: width=60, height=50
		{"narrow 60x50", 60, 50, overviewParams{topN: 3, recentN: 3, showActivity: true, showRow2: true, columns: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &model{width: tc.width, height: tc.height}
			got := m.overviewLayout()
			if got != tc.want {
				t.Errorf("overviewLayout() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

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
	m := NewModel(context.Background(), "", "", "", "", 24*time.Hour)
	m.hookLogsDir = "/mock/home/.kapm/logs"
	m.homeDir = "/mock/home"
	m.metrics = fixture()
	m.width = 140
	m.height = 40
	m.recomputeSummaryTotals()
	return m
}

func press(m *model, key string) *model {
	next, _ := m.handleKey(key)
	return next.(*model)
}

func TestTUIOverviewRender(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	out := m.renderView()
	for _, want := range []string{"kapm monitor", "Overview", "Sessions", "Agents", "Tools", "Summary", "Top tools", "Top agents", "Activity"} {
		if !strings.Contains(out, want) {
			t.Errorf("overview missing %q", want)
		}
	}
}

func TestTUIOverviewShowsKiroUsageInsteadOfAggregateCredits(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	m.metrics.Overview.Sessions[0].TotalCredits = 42
	m.kiroUsage = &kirocliusage.Usage{ResetDate: "2026-06-01", Plan: "KIRO FREE", UsedCredits: 6.72, TotalCredits: 50, Percent: 13, Overages: "Disabled"}

	out := m.renderOverview()
	for _, want := range []string{"kiro usage: 6.72 / 50", "usage:      13% · resets 2026-06-01", "plan:       KIRO FREE · overages disabled"} {
		if !strings.Contains(out, want) {
			t.Fatalf("overview missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "credits:") || strings.Contains(out, "42.00") {
		t.Fatalf("overview still shows aggregate credits:\n%s", out)
	}
}

func TestTUIOverviewShowsKiroUsageCheckingWhenUnavailable(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	m.metrics.Overview.Sessions[0].TotalCredits = 42
	m.kiroUsageRead = func(context.Context) (kirocliusage.Usage, bool, error) { return kirocliusage.Usage{}, false, nil }

	out := m.renderOverview()
	if !strings.Contains(out, "kiro usage:") || !strings.Contains(out, "checking") {
		t.Fatalf("overview should show Kiro Usage checking placeholder:\n%s", out)
	}
	if strings.Contains(out, "credits:") || strings.Contains(out, "42.00") {
		t.Fatalf("overview should omit aggregate credits when Kiro Usage unavailable:\n%s", out)
	}
}

func TestTUIKiroUsageCmdCachesKiroUsage(t *testing.T) {
	t.Parallel()
	m := NewModel(context.Background(), t.TempDir(), t.TempDir(), "", "", 24*time.Hour)
	m.kiroUsageTTL = time.Hour
	var calls atomic.Int32
	m.kiroUsageRead = func(context.Context) (kirocliusage.Usage, bool, error) {
		calls.Add(1)
		return kirocliusage.Usage{ResetDate: "2026-06-01", Plan: "KIRO FREE", UsedCredits: 6.72, TotalCredits: 50, Percent: 13}, true, nil
	}

	cmd := m.kiroUsageCmd()
	if cmd == nil {
		t.Fatal("kiroUsageCmd() = nil, want command")
	}
	msg, ok := cmd().(kiroUsageMsg)
	if !ok {
		t.Fatalf("kiroUsageCmd() returned %T, want kiroUsageMsg", msg)
	}
	if msg.usage == nil || msg.usage.CreditLabel() != "6.72 / 50" {
		t.Fatalf("kiroUsageCmd() usage = %+v", msg.usage)
	}
	m.Update(msg)

	if cmd := m.kiroUsageCmd(); cmd != nil {
		t.Fatal("second kiroUsageCmd() returned command inside TTL, want nil")
	}
	if calls.Load() != 1 {
		t.Fatalf("usage reader calls = %d, want 1", calls.Load())
	}
}

func TestTUIRefreshDoesNotWaitForSlowKiroUsage(t *testing.T) {
	t.Parallel()
	m := NewModel(context.Background(), t.TempDir(), t.TempDir(), "", "", 24*time.Hour)
	m.kiroUsageRead = func(context.Context) (kirocliusage.Usage, bool, error) {
		t.Fatal("refreshCmd must not call Kiro Usage reader")
		return kirocliusage.Usage{}, false, nil
	}

	begin := time.Now()
	msg, ok := m.refreshCmd()().(metricsMsg)
	if !ok {
		t.Fatalf("refreshCmd() returned %T, want metricsMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("refreshCmd() error = %v", msg.err)
	}
	if elapsed := time.Since(begin); elapsed > 50*time.Millisecond {
		t.Fatalf("refreshCmd() blocked for %s", elapsed)
	}
}

func TestTUIKiroUsageRefreshKeepsStaleOnFailure(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	m.kiroUsageRead = func(context.Context) (kirocliusage.Usage, bool, error) { return kirocliusage.Usage{}, false, nil }
	m.kiroUsage = &kirocliusage.Usage{ResetDate: "2026-06-01", Plan: "KIRO FREE", UsedCredits: 6.72, TotalCredits: 50, Percent: 13}
	m.kiroUsageFetchedAt = time.Now().Add(-2 * defaultKiroUsageTTL)

	cmd := m.kiroUsageCmd()
	if cmd == nil {
		t.Fatal("kiroUsageCmd() = nil, want refresh command")
	}
	msg, ok := cmd().(kiroUsageMsg)
	if !ok {
		t.Fatalf("kiroUsageCmd() returned %T, want kiroUsageMsg", msg)
	}
	m.Update(msg)
	if m.kiroUsage == nil || m.kiroUsage.Plan != "KIRO FREE" {
		t.Fatalf("stale usage was cleared after failed refresh: %+v", m.kiroUsage)
	}
}

func TestTUITabsSwitchViaTab(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	m := newTestModel()
	m = press(m, "4") // Tools
	m = press(m, "enter")
	out := m.renderView()
	if strings.Contains(out, "Aliases") {
		t.Errorf("tool detail should hide aliases section for single observed tool name")
	}
	if !strings.Contains(out, "Recent calls") || !strings.Contains(out, "Error samples") {
		t.Errorf("tool detail missing sections")
	}
}

func TestTUIToolDetailShowsAliasDistributionAndRawTool(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	m := newTestModel()
	m.metrics.Tools = []ToolDetail{{
		ToolMetric: ToolMetric{Name: "read", CallCount: 2},
		Aliases: []ToolAliasMetric{
			{Name: "read", CallCount: 1, Percentage: 0.5},
			{Name: "fs_read", CallCount: 1, Percentage: 0.5},
		},
		RecentCalls: []ToolCall{{Ts: start, Session: "abcdef123456789012", Agent: "lead", Tool: "fs_read", Duration: JSONDuration(78 * time.Millisecond)}},
	}}
	m = press(m, "4") // Tools
	m = press(m, "enter")

	out := m.renderView()
	for _, want := range []string{"Aliases", "fs_read", "50.0%", "Tool", "Recent calls"} {
		if !strings.Contains(out, want) {
			t.Fatalf("tool detail missing %q in output:\n%s", want, out)
		}
	}
}

func TestTUIToolDetailDisplaysUnknownDurationsAsDash(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	m := newTestModel()
	m.metrics.Tools = []ToolDetail{{
		ToolMetric:  ToolMetric{Name: "read", CallCount: 1},
		RecentCalls: []ToolCall{{Ts: start, Session: "abcdef123456789012", Agent: "lead", Tool: "read"}},
	}}
	m = press(m, "4")
	m = press(m, "enter")

	out := m.renderView()
	if !strings.Contains(out, "avg duration: -") {
		t.Fatalf("tool detail should render unknown average duration as dash:\n%s", out)
	}
	if !regexp.MustCompile(`lead\s+-`).MatchString(out) {
		t.Fatalf("recent call should render zero duration as dash:\n%s", out)
	}
}

func TestTUIBackFromDetail(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	m.tab = tabSessions
	m.detail = true
	m = press(m, "esc")
	if m.detail {
		t.Errorf("expected detail=false after esc")
	}
}

func TestTUIDownUpSelection(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	m := NewModel(context.Background(), "", "/nowhere", "", "", time.Hour)
	m.width, m.height = 100, 30
	out := m.renderView()
	if !strings.Contains(out, "Loading sessions") {
		t.Errorf("expected loading message, got: %s", out)
	}
}

func TestTUIHelpLineChanges(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	m := newTestModel()
	m.metrics.Skills = nil
	out := m.renderView()
	if strings.Contains(out, "Skills (reads)") {
		t.Errorf("overview should hide Skills box when no skills")
	}
}

func TestTUISessionsListHasLastActivityColumn(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	m.tab = tabSessions
	out := m.renderView()
	if !strings.Contains(out, "Last act") {
		t.Errorf("sessions list missing Last activity column header")
	}
}

func TestTUISessionsListHasFilesColumn(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	m.tab = tabSessions
	out := m.renderView()
	if !strings.Contains(out, "Files") {
		t.Errorf("sessions list missing Files column header")
	}
}

func TestTUIToolDetailErrorHasInput(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	m := newTestModel()
	m.height = 50 // avail=44 → topN=10
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
	t.Parallel()
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
	recent := m.renderRecentSessionsBox(m.contentWidth(), maxRecentSessions)
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	m := NewModel(context.Background(), "", ".", "", "", time.Hour)
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
	t.Parallel()
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
	t.Parallel()
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
	fakeHome := filepath.Join(t.TempDir(), "fakehome")
	// abbrevHome uses the passed-in home, not os.UserHomeDir.
	childPath := filepath.Join(fakeHome, "foo")
	if got, want := abbrevHome(fakeHome, childPath), "~"+string(filepath.Separator)+"foo"; got != want {
		t.Errorf("abbrevHome(%q, %q) = %q, want %q", fakeHome, childPath, got, want)
	}
	if got := abbrevHome(fakeHome, fakeHome); got != "~" {
		t.Errorf("abbrevHome(%q, %q) = %q, want %q", fakeHome, fakeHome, got, "~")
	}
	otherPath := filepath.Join(t.TempDir(), "other", "path")
	if got := abbrevHome(fakeHome, otherPath); got != otherPath {
		t.Errorf("abbrevHome(%q, %q) = %q, want %q", fakeHome, otherPath, got, otherPath)
	}
	somePath := filepath.Join(t.TempDir(), "some", "path")
	if got := abbrevHome("", somePath); got != somePath {
		t.Errorf("abbrevHome(%q, %q) = %q, want %q", "", somePath, got, somePath)
	}

	// Verify model stores homeDir once at construction.
	t.Setenv("HOME", fakeHome)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", fakeHome)
		t.Setenv("HOMEDRIVE", "")
		t.Setenv("HOMEPATH", "")
	}
	m := NewModel(context.Background(), "", filepath.Join(t.TempDir(), "logs"), "", "", 24*time.Hour)
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
	t.Parallel()
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

	var orchLine, leadLine string
	for l := range strings.SplitSeq(out, "\n") {
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
	// Second row: agent cell should be vertically aligned (no extra indent).
	if !strings.Contains(leadLine, "lead") {
		t.Errorf("second row should contain agent name, got: %q", leadLine)
	}
}

func TestTUISessionsListKeepsSessionCredits(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	m.metrics.Sessions[0].TotalCredits = 1.25
	m.tab = tabSessions
	out := m.renderSessionsList()
	if !strings.Contains(out, "1.25") {
		t.Fatalf("sessions list missing session credit value:\n%s", out)
	}
}

func TestTUIRenderSessionDetailTitle(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	m := newTestModel()
	base := fixture()
	sid := "shared-session-id"
	base.Sessions = []SessionDetail{
		{SessionMetric: SessionMetric{ID: sid, Agent: "orchestrator", LastActivity: time.Now()}},
		{SessionMetric: SessionMetric{ID: sid, Agent: "lead", LastActivity: time.Now().Add(-time.Second)}},
	}
	m.metrics = base
	out := m.renderRecentSessionsBox(m.contentWidth(), maxRecentSessions)

	var orchLine, leadLine string
	for l := range strings.SplitSeq(out, "\n") {
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
	// Second row: agent cell should be vertically aligned (no extra indent).
	if !strings.Contains(leadLine, "lead") {
		t.Errorf("second row should contain agent name, got: %q", leadLine)
	}
}

func TestTUIAgentDetailTitleColumn(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	m = press(m, "3") // Agents tab
	m = press(m, "enter")
	out := m.renderView()
	if !strings.Contains(out, "Title") {
		t.Errorf("agent detail sessions table missing 'Title' column header")
	}
}

func TestTUISessionsListTitleTruncation(t *testing.T) {
	t.Parallel()
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

// stripANSI removes ANSI escape sequences from s for plain-text assertions.
func stripANSI(s string) string {
	re := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return re.ReplaceAllString(s, "")
}

// --- renderSessionChanges tests ---

func TestTUIRenderSessionChanges_Empty(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	base.Sessions[0].Changes = nil
	m.metrics = base
	out := m.renderSessionChanges(&m.metrics.Sessions[0])
	if strings.Contains(out, "▸ Changes") {
		t.Errorf("expected no Changes section when Changes is empty, got: %s", out)
	}
}

func TestTUIRenderSessionChanges_SectionHeader_Singular(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "create", Purpose: "init"},
	}
	base.Sessions[0].FilesChanged = 1
	m.metrics = base
	out := m.renderSessionChanges(&m.metrics.Sessions[0])
	if !strings.Contains(out, "(1 file, 1 edit)") {
		t.Errorf("expected '(1 file, 1 edit)' in output, got: %s", out)
	}
}

func TestTUIRenderSessionChanges_SectionHeader_Plural(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "create", Purpose: "first"},
		{Path: "/tmp/b.go", Ts: ts.Add(time.Second), Command: "strReplace", Purpose: "second"},
		{Path: "/tmp/a.go", Ts: ts.Add(2 * time.Second), Command: "strReplace", Purpose: "third"},
	}
	base.Sessions[0].FilesChanged = 2
	m.metrics = base
	out := m.renderSessionChanges(&m.metrics.Sessions[0])
	if !strings.Contains(out, "(2 files, 3 edits)") {
		t.Errorf("expected '(2 files, 3 edits)' in output, got: %s", out)
	}
}

func TestTUIRenderSessionChanges_SinglePath(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "create", Purpose: "purpose text"},
	}
	base.Sessions[0].FilesChanged = 1
	m.metrics = base
	out := stripANSI(m.renderSessionChanges(&m.metrics.Sessions[0]))
	if !strings.Contains(out, "1 edit") {
		t.Errorf("expected '1 edit' in output, got: %s", out)
	}
	if !strings.Contains(out, "[create]") {
		t.Errorf("expected '[create]' in output, got: %s", out)
	}
	if !strings.Contains(out, `"purpose text"`) {
		t.Errorf("expected '\"purpose text\"' in output, got: %s", out)
	}
}

func TestTUIRenderSessionChanges_MultiPath_SortLastTsDesc(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	// a.go lastTs=10:00, b.go lastTs=12:00, c.go lastTs=11:00 → order: b, c, a
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "create", Purpose: "a"},
		{Path: "/tmp/b.go", Ts: ts.Add(2 * time.Second), Command: "create", Purpose: "b"},
		{Path: "/tmp/c.go", Ts: ts.Add(time.Second), Command: "create", Purpose: "c"},
	}
	base.Sessions[0].FilesChanged = 3
	m.metrics = base
	out := m.renderSessionChanges(&m.metrics.Sessions[0])
	idxA := strings.Index(out, "a.go")
	idxB := strings.Index(out, "b.go")
	idxC := strings.Index(out, "c.go")
	if idxA < 0 || idxB < 0 || idxC < 0 {
		t.Fatalf("expected a.go, b.go, c.go in output, got: %s", out)
	}
	// lastTs desc: b.go (ts+2s) > c.go (ts+1s) > a.go (ts)
	if idxB >= idxC || idxC >= idxA {
		t.Errorf("expected lastTs desc order b.go < c.go < a.go (by position), got indices %d %d %d", idxB, idxC, idxA)
	}
}

func TestTUIRenderSessionChanges_SortTiebreakAlphabetical(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	// zeta.go and alpha.go both have same lastTs → alphabetical tiebreak
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/zeta.go", Ts: ts, Command: "create", Purpose: "z"},
		{Path: "/tmp/alpha.go", Ts: ts, Command: "create", Purpose: "a"},
	}
	base.Sessions[0].FilesChanged = 2
	m.metrics = base
	out := m.renderSessionChanges(&m.metrics.Sessions[0])
	idxAlpha := strings.Index(out, "alpha.go")
	idxZeta := strings.Index(out, "zeta.go")
	if idxAlpha < 0 || idxZeta < 0 {
		t.Fatalf("expected alpha.go and zeta.go in output, got: %s", out)
	}
	if idxAlpha >= idxZeta {
		t.Errorf("expected alphabetical tiebreak: alpha.go before zeta.go, got indices %d %d", idxAlpha, idxZeta)
	}
}

func TestTUIRenderSessionChanges_ShellWarning(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "create", Purpose: "init"},
	}
	base.Sessions[0].FilesChanged = 1
	base.Sessions[0].Timeline = append(base.Sessions[0].Timeline, EventEntry{
		Ts: ts, Event: apmconfig.EventPreToolUse, Tool: apmconfig.ToolShell,
	})
	base.Sessions[0].HasShell = true
	m.metrics = base
	out := m.renderSessionChanges(&m.metrics.Sessions[0])
	if !strings.Contains(out, "also ran shell") {
		t.Errorf("expected shell warning in output, got: %s", out)
	}
}

func TestTUIRenderSessionChanges_NoShellWarning_WhenNoShell(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "create", Purpose: "init"},
	}
	base.Sessions[0].FilesChanged = 1
	// Ensure no shell events in timeline.
	base.Sessions[0].Timeline = []EventEntry{
		{Ts: ts, Event: apmconfig.EventPreToolUse, Tool: "write"},
	}
	m.metrics = base
	out := m.renderSessionChanges(&m.metrics.Sessions[0])
	if strings.Contains(out, "also ran shell") {
		t.Errorf("expected no shell warning when no shell events, got: %s", out)
	}
}

func TestTUIRenderSessionChanges_Oversized(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "strReplace", Purpose: "big edit", Oversized: true},
	}
	base.Sessions[0].FilesChanged = 1
	m.metrics = base
	out := stripANSI(m.renderSessionChanges(&m.metrics.Sessions[0]))
	if !strings.Contains(out, "(oversized — diff unavailable)") {
		t.Errorf("expected '(oversized — diff unavailable)' in output, got: %s", out)
	}
	// No +/- badge for fully-oversized file.
	if strings.Contains(out, "+0/-0") || strings.Contains(out, "+0/") {
		t.Errorf("expected no +/- badge for oversized edit, got: %s", out)
	}
}

func TestTUIRenderSessionChanges_NoPurpose(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "strReplace", Purpose: ""},
	}
	base.Sessions[0].FilesChanged = 1
	m.metrics = base
	out := stripANSI(m.renderSessionChanges(&m.metrics.Sessions[0]))
	if !strings.Contains(out, "(no purpose)") {
		t.Errorf("expected '(no purpose)' in output, got: %s", out)
	}
}

func TestTUIRenderSessionChanges_SeeTimelineHint(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "create", Purpose: "init"},
	}
	base.Sessions[0].FilesChanged = 1
	m.metrics = base
	out := m.renderSessionChanges(&m.metrics.Sessions[0])
	if !strings.Contains(out, "see Timeline for full event order") {
		t.Errorf("expected 'see Timeline for full event order' in output, got: %s", out)
	}
}

func TestTUIRenderSessionChanges_MultipleEditsPluralForm(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "strReplace", Purpose: "first"},
		{Path: "/tmp/a.go", Ts: ts.Add(time.Second), Command: "strReplace", Purpose: "second"},
	}
	base.Sessions[0].FilesChanged = 1
	m.metrics = base
	out := m.renderSessionChanges(&m.metrics.Sessions[0])
	if !strings.Contains(out, "2 edits") {
		t.Errorf("expected '2 edits' in output, got: %s", out)
	}
}

func TestTUIRenderSessionChanges_LastTimestampFormat(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "create", Purpose: "init"},
	}
	base.Sessions[0].FilesChanged = 1
	m.metrics = base
	out := m.renderSessionChanges(&m.metrics.Sessions[0])
	re := regexp.MustCompile(`last \d{2}:\d{2}:\d{2}`)
	if !re.MatchString(out) {
		t.Errorf("expected 'last HH:MM:SS' pattern in output, got: %s", out)
	}
}

func TestTUIRenderSessionHeader_IncludesFilesCount(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	base.Sessions[0].FilesChanged = 5
	m.metrics = base
	out := m.renderSessionHeader(&m.metrics.Sessions[0])
	if !strings.Contains(out, "files: 5") {
		t.Errorf("expected 'files: 5' in header, got: %s", out)
	}
}

func TestTUIRenderSessionHeaderKeepsSessionCredits(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	base.Sessions[0].TotalCredits = 1.25
	m.metrics = base
	out := m.renderSessionHeader(&m.metrics.Sessions[0])
	if !strings.Contains(out, "credits: 1.25") {
		t.Fatalf("session header missing session credits:\n%s", out)
	}
}

func TestTUIRenderSessionDetail_SectionOrder(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	base.Sessions[0].AssistantResponse = "done"
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "create", Purpose: "init"},
	}
	base.Sessions[0].FilesChanged = 1
	m.metrics = base
	m = press(m, "2")
	m = press(m, "enter")
	out := m.renderView()
	idxResult := strings.Index(out, "Session Result")
	idxChanges := strings.Index(out, "▸ Changes")
	idxPrompts := strings.Index(out, "▸ Prompts")
	if idxResult < 0 || idxChanges < 0 || idxPrompts < 0 {
		t.Fatalf("missing sections: Session Result=%d, ▸ Changes=%d, ▸ Prompts=%d\noutput:\n%s", idxResult, idxChanges, idxPrompts, out)
	}
	if idxResult >= idxChanges || idxChanges >= idxPrompts {
		t.Errorf("expected Session Result < ▸ Changes < ▸ Prompts, got indices %d %d %d", idxResult, idxChanges, idxPrompts)
	}
}

// --- Task 5 QA scenario tests ---

func TestTUIRenderSessionChanges_CountsPerEditAndFile(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	// create: 3 lines added; strReplace: 2 deleted / 1 added → file total +4/-2
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "create", Purpose: "init",
			Content: "line1\nline2\nline3\n"},
		{Path: "/tmp/a.go", Ts: ts.Add(time.Second), Command: "strReplace", Purpose: "fix",
			OldStr: "line1\nline2\n", NewStr: "replaced\n"},
	}
	base.Sessions[0].FilesChanged = 1
	m.metrics = base
	out := stripANSI(m.renderSessionChanges(&m.metrics.Sessions[0]))
	// Per-edit counts.
	if !strings.Contains(out, "+3/-0") {
		t.Errorf("expected '+3/-0' for create edit, got:\n%s", out)
	}
	if !strings.Contains(out, "+1/-2") {
		t.Errorf("expected '+1/-2' for strReplace edit, got:\n%s", out)
	}
	// File aggregate: +4/-2.
	if !strings.Contains(out, "+4/-2") {
		t.Errorf("expected '+4/-2' aggregate for file, got:\n%s", out)
	}
}

func TestTUIRenderSessionChanges_PreviewTruncation(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	m.changesExpanded = true
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	// 100-line create.
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&sb, "line%d\n", i)
	}
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "create", Purpose: "big", Content: sb.String()},
	}
	base.Sessions[0].FilesChanged = 1
	m.metrics = base
	out := stripANSI(m.renderSessionChanges(&m.metrics.Sessions[0]))
	// Count preview lines (lines starting with 9 spaces + "+").
	previewLines := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, previewIndent+"+") {
			previewLines++
		}
	}
	if previewLines > 32 {
		t.Errorf("expected at most 32 preview lines, got %d", previewLines)
	}
	if !strings.Contains(out, "more lines") {
		t.Errorf("expected '…N more lines' truncation marker, got:\n%s", out)
	}
}

func TestTUIRenderSessionChanges_OversizedFallback(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "create", Purpose: "big", Oversized: true},
	}
	base.Sessions[0].FilesChanged = 1
	m.metrics = base
	out := stripANSI(m.renderSessionChanges(&m.metrics.Sessions[0]))
	// Must show the oversized message.
	if !strings.Contains(out, "(oversized — diff unavailable)") {
		t.Errorf("expected '(oversized — diff unavailable)', got:\n%s", out)
	}
	// Must NOT show +/- badge on edit line.
	for l := range strings.SplitSeq(out, "\n") {
		if strings.Contains(l, "• [create]") && (strings.Contains(l, "+0") || strings.Contains(l, "-0")) {
			t.Errorf("oversized edit line should not have +/- badge: %q", l)
		}
	}
	// File line must show — (muted dash) not a numeric count.
	if strings.Contains(out, "+0/-0") {
		t.Errorf("fully-oversized file should not show +0/-0, got:\n%s", out)
	}
}

func TestTUIRenderSessionChanges_PartialOversized(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "create", Purpose: "ok", Content: "line1\nline2\n"},
		{Path: "/tmp/a.go", Ts: ts.Add(time.Second), Command: "strReplace", Purpose: "big", Oversized: true},
	}
	base.Sessions[0].FilesChanged = 1
	m.metrics = base
	out := stripANSI(m.renderSessionChanges(&m.metrics.Sessions[0]))
	// File aggregate should sum only OK edits (+2/-0).
	if !strings.Contains(out, "+2/-0") {
		t.Errorf("expected '+2/-0' for partial-oversized file, got:\n%s", out)
	}
	// Should show (1 oversized) annotation.
	if !strings.Contains(out, "(1 oversized)") {
		t.Errorf("expected '(1 oversized)' annotation, got:\n%s", out)
	}
}

func TestTUIRenderSessionChanges_ColorCodes(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	m.changesExpanded = true
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "strReplace", Purpose: "fix",
			OldStr: "old\n", NewStr: "new\n"},
	}
	base.Sessions[0].FilesChanged = 1
	m.metrics = base
	out := m.renderSessionChanges(&m.metrics.Sessions[0])
	// With lipgloss default rendering, ANSI codes may or may not be present
	// depending on the terminal profile. We verify the raw output contains
	// the styled content by checking that stripping ANSI gives expected text.
	plain := stripANSI(out)
	if !strings.Contains(plain, "+new") {
		t.Errorf("expected '+new' in diff preview, got:\n%s", plain)
	}
	if !strings.Contains(plain, "-old") {
		t.Errorf("expected '-old' in diff preview, got:\n%s", plain)
	}
}

func TestTUIRenderSessionChanges_DiffHiddenByDefault(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	// changesExpanded defaults to false.
	base := fixture()
	ts := time.Date(2026, 4, 20, 12, 30, 5, 0, time.UTC)
	base.Sessions[0].Changes = []FileChange{
		{Path: "/tmp/a.go", Ts: ts, Command: "strReplace", Purpose: "fix",
			OldStr: "old\n", NewStr: "new\n"},
	}
	base.Sessions[0].FilesChanged = 1
	m.metrics = base
	out := stripANSI(m.renderSessionChanges(&m.metrics.Sessions[0]))
	// +N/-M counts must still appear, but raw diff preview lines must not.
	if !strings.Contains(out, "+1") || !strings.Contains(out, "-1") {
		t.Errorf("expected +1/-1 counts, got:\n%s", out)
	}
	if strings.Contains(out, previewIndent+"+new") || strings.Contains(out, previewIndent+"-old") {
		t.Errorf("diff preview should be hidden by default, got:\n%s", out)
	}
}

func TestRecomputeDetailCacheInvariant(t *testing.T) {
	t.Parallel()

	// Non-empty body: len(cachedDetail.lines) == strings.Count(body, "\n") + 1.
	m := newTestModel()
	m = press(m, "2")     // sessions tab
	m = press(m, "enter") // enter detail — triggers recomputeDetailCache
	if !m.detail {
		t.Fatal("expected detail mode")
	}
	body := m.cachedDetail.body
	if body == "" {
		t.Fatal("expected non-empty cachedDetail.body in session detail")
	}
	want := strings.Count(body, "\n") + 1
	if got := len(m.cachedDetail.lines); got != want {
		t.Errorf("non-empty body: len(cachedDetail.lines) = %d, want %d", got, want)
	}

	// Empty body: cachedDetail.lines must be nil.
	m2 := newTestModel()
	// detail=false → recomputeDetailCache clears everything.
	m2.detail = false
	m2.recomputeDetailCache()
	if m2.cachedDetail.lines != nil {
		t.Errorf("empty body: expected cachedDetail.lines == nil, got %v", m2.cachedDetail.lines)
	}
}

// TestTUISessionsOnlyMode verifies that the TUI renders correctly when
// hookLogsDir is empty (sessions-only mode, no hook log data).
func TestTUISessionsOnlyMode(t *testing.T) {
	t.Parallel()
	// NewModel with empty hookLogsDir simulates sessions-only mode.
	m := NewModel(context.Background(), t.TempDir(), "", "", "", 24*time.Hour)
	m.metrics = fixture()
	m.width = 140
	m.height = 40
	out := m.renderView()
	// Basic rendering must succeed.
	for _, want := range []string{"kapm monitor", "Overview", "Sessions"} {
		if !strings.Contains(out, want) {
			t.Errorf("sessions-only mode missing %q in output", want)
		}
	}
	// Sessions list must show session IDs.
	m = press(m, "2")
	out = m.renderView()
	if !strings.Contains(out, "abcdef123456") {
		t.Errorf("sessions-only mode: sessions tab missing session ID prefix")
	}
}

func newOverflowModel() *model {
	m := newTestModel()
	m.height = 10 // overviewViewportHeight = max(10-7,5) = 5; overview has many more lines
	return m
}

func TestOverviewScrollFooterAppearsOnOverflow(t *testing.T) {
	t.Parallel()
	m := newOverflowModel()
	out := m.renderBody()
	if !strings.Contains(out, "more lines, ↓ to scroll") {
		t.Errorf("expected scroll footer in overflow overview, got:\n%s", out)
	}
}

func TestOverviewScrollNoFooterWhenFits(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	m.height = 200 // viewport = max(200-7,5) = 193; overview fits
	out := m.renderBody()
	if strings.Contains(out, "more lines, ↓ to scroll") {
		t.Errorf("unexpected scroll footer when content fits:\n%s", out)
	}
}

func TestOverviewScrollJKKeys(t *testing.T) {
	t.Parallel()
	m := newOverflowModel()
	m = press(m, "j")
	m = press(m, "j")
	m = press(m, "j")
	if m.overviewScroll != 3 {
		t.Errorf("expected overviewScroll=3 after 3×j, got %d", m.overviewScroll)
	}
	m = press(m, "k")
	if m.overviewScroll != 2 {
		t.Errorf("expected overviewScroll=2 after k, got %d", m.overviewScroll)
	}
}

func TestOverviewScrollGKeys(t *testing.T) {
	t.Parallel()
	m := newOverflowModel()
	m = press(m, "j")
	m = press(m, "j")
	m = press(m, "g")
	if m.overviewScroll != 0 {
		t.Errorf("expected overviewScroll=0 after g, got %d", m.overviewScroll)
	}
	m = press(m, "G")
	// renderBody lazy-clamps; trigger it
	m.renderBody()
	if m.overviewScroll <= 0 {
		t.Errorf("expected overviewScroll>0 after G+renderBody, got %d", m.overviewScroll)
	}
}

func TestOverviewScrollPersistsAcrossTabSwitch(t *testing.T) {
	t.Parallel()
	m := newOverflowModel()
	m = press(m, "j")
	m = press(m, "j")
	m = press(m, "j")
	m = press(m, "j")
	m = press(m, "j")
	if m.overviewScroll != 5 {
		t.Errorf("expected overviewScroll=5, got %d", m.overviewScroll)
	}
	m = press(m, "2") // switch to Sessions
	m = press(m, "1") // switch back to Overview
	if m.overviewScroll != 5 {
		t.Errorf("expected overviewScroll=5 after tab round-trip, got %d", m.overviewScroll)
	}
}

func TestOverviewScrollKDoesNotGoNegative(t *testing.T) {
	t.Parallel()
	m := newOverflowModel()
	m = press(m, "k")
	if m.overviewScroll != 0 {
		t.Errorf("expected overviewScroll=0 after k at top, got %d", m.overviewScroll)
	}
}
