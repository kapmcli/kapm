package monitor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

var baseTime = time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)

func rec(session, agent, event, tool string, offset time.Duration) Record {
	return Record{
		Ts:      baseTime.Add(offset),
		Session: session,
		Agent:   agent,
		Event:   event,
		Tool:    tool,
	}
}

func aggregateOverview(records []Record, now time.Time) Metrics {
	dm, _ := AggregateDetail(context.Background(), records, now)
	return dm.Overview
}

// mustAggregate fails the test if AggregateDetail returns an error.
func mustAggregate(t *testing.T, records []Record, now time.Time) DetailedMetrics {
	t.Helper()
	dm, err := AggregateDetail(context.Background(), records, now)
	if err != nil {
		t.Fatalf("AggregateDetail: %v", err)
	}
	return dm
}

func TestAggregateEmpty(t *testing.T) {
	t.Parallel()
	m := aggregateOverview(nil, baseTime)
	if m.Sessions != nil || m.Tools != nil || m.Agents != nil || m.HourlyActivity != nil {
		t.Errorf("expected zero-value Metrics, got %+v", m)
	}
	m2 := aggregateOverview([]Record{}, baseTime)
	if m2.Sessions != nil || m2.Tools != nil || m2.Agents != nil || m2.HourlyActivity != nil {
		t.Errorf("expected zero-value Metrics for empty slice, got %+v", m2)
	}
}

func TestAggregateSession(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(10 * time.Minute)
	records := []Record{
		rec("s1", "agent1", apmconfig.EventAgentSpawn, "", 0),
		rec("s1", "agent1", apmconfig.EventUserPromptSubmit, "", 1*time.Minute),
		rec("s1", "agent1", apmconfig.EventPreToolUse, "bash", 2*time.Minute),
		rec("s1", "agent1", apmconfig.EventPostToolUse, "bash", 3*time.Minute),
		rec("s1", "agent1", apmconfig.EventStop, "", 4*time.Minute),
		// s2: active (no stop, last event 2min before now)
		rec("s2", "agent2", apmconfig.EventAgentSpawn, "", 5*time.Minute),
		rec("s2", "agent2", apmconfig.EventUserPromptSubmit, "", 8*time.Minute),
	}

	m := aggregateOverview(records, now)

	byID := map[string]SessionMetric{}
	for _, s := range m.Sessions {
		byID[s.ID] = s
	}

	s1 := byID["s1"]
	if s1.Active {
		t.Error("s1 should not be active (has stop event)")
	}
	if s1.ToolCalls != 1 {
		t.Errorf("s1 ToolCalls: want 1, got %d", s1.ToolCalls)
	}
	if s1.Prompts != 1 {
		t.Errorf("s1 Prompts: want 1, got %d", s1.Prompts)
	}
	wantDur := 4 * time.Minute
	if s1.Duration != JSONDuration(wantDur) {
		t.Errorf("s1 Duration: want %v, got %v", wantDur, s1.Duration)
	}

	s2 := byID["s2"]
	if !s2.Active {
		t.Error("s2 should be active (no stop, last event within 5min)")
	}
	if s2.Prompts != 1 {
		t.Errorf("s2 Prompts: want 1, got %d", s2.Prompts)
	}
}

func TestAggregateSession_InactiveOld(t *testing.T) {
	t.Parallel()
	// session with no stop but last event > 5min ago
	now := baseTime.Add(20 * time.Minute)
	records := []Record{
		rec("s1", "agent1", apmconfig.EventAgentSpawn, "", 0),
		rec("s1", "agent1", apmconfig.EventUserPromptSubmit, "", 1*time.Minute),
	}
	m := aggregateOverview(records, now)
	if len(m.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(m.Sessions))
	}
	if m.Sessions[0].Active {
		t.Error("session should be inactive (last event > 5min ago)")
	}
}

func TestAggregateTool(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []Record{
		// bash: 2 calls, 1 error (second preToolUse has no postToolUse)
		rec("s1", "a", apmconfig.EventPreToolUse, "bash", 0),
		rec("s1", "a", apmconfig.EventPostToolUse, "bash", 1*time.Minute),
		rec("s1", "a", apmconfig.EventPreToolUse, "bash", 2*time.Minute),
		// no postToolUse for second bash call
		// grep: 1 call, 0 errors
		rec("s1", "a", apmconfig.EventPreToolUse, "grep", 3*time.Minute),
		rec("s1", "a", apmconfig.EventPostToolUse, "grep", 4*time.Minute),
	}

	m := aggregateOverview(records, now)

	byName := map[string]ToolMetric{}
	for _, tm := range m.Tools {
		byName[tm.Name] = tm
	}

	bash := byName["bash"]
	if bash.CallCount != 2 {
		t.Errorf("bash CallCount: want 2, got %d", bash.CallCount)
	}
	if bash.ErrorCount != 1 {
		t.Errorf("bash ErrorCount: want 1, got %d", bash.ErrorCount)
	}
	if bash.ErrorRate != 0.5 {
		t.Errorf("bash ErrorRate: want 0.5, got %f", bash.ErrorRate)
	}

	grep := byName["grep"]
	if grep.CallCount != 1 {
		t.Errorf("grep CallCount: want 1, got %d", grep.CallCount)
	}
	if grep.ErrorCount != 0 {
		t.Errorf("grep ErrorCount: want 0, got %d", grep.ErrorCount)
	}
	if grep.ErrorRate != 0 {
		t.Errorf("grep ErrorRate: want 0, got %f", grep.ErrorRate)
	}
}

func TestAggregateAgent(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []Record{
		// agent1: 2 sessions
		rec("s1", "agent1", apmconfig.EventAgentSpawn, "", 0),
		rec("s1", "agent1", apmconfig.EventUserPromptSubmit, "", 1*time.Minute),
		rec("s1", "agent1", apmconfig.EventPreToolUse, "bash", 2*time.Minute),
		rec("s1", "agent1", apmconfig.EventPostToolUse, "bash", 3*time.Minute),
		rec("s2", "agent1", apmconfig.EventAgentSpawn, "", 5*time.Minute),
		rec("s2", "agent1", apmconfig.EventUserPromptSubmit, "", 6*time.Minute),
		rec("s2", "agent1", apmconfig.EventUserPromptSubmit, "", 7*time.Minute),
		// agent2: 1 session
		rec("s3", "agent2", apmconfig.EventAgentSpawn, "", 0),
		rec("s3", "agent2", apmconfig.EventPreToolUse, "grep", 1*time.Minute),
		rec("s3", "agent2", apmconfig.EventPostToolUse, "grep", 2*time.Minute),
	}

	m := aggregateOverview(records, now)

	byName := map[string]AgentMetric{}
	for _, a := range m.Agents {
		byName[a.Name] = a
	}

	a1 := byName["agent1"]
	if a1.SessionCount != 2 {
		t.Errorf("agent1 SessionCount: want 2, got %d", a1.SessionCount)
	}
	if a1.ToolCalls != 1 {
		t.Errorf("agent1 ToolCalls: want 1, got %d", a1.ToolCalls)
	}
	if a1.Prompts != 3 {
		t.Errorf("agent1 Prompts: want 3, got %d", a1.Prompts)
	}

	a2 := byName["agent2"]
	if a2.SessionCount != 1 {
		t.Errorf("agent2 SessionCount: want 1, got %d", a2.SessionCount)
	}
	if a2.ToolCalls != 1 {
		t.Errorf("agent2 ToolCalls: want 1, got %d", a2.ToolCalls)
	}
}

func TestAggregateHourly(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(3 * time.Hour)
	// baseTime = 10:00 UTC
	// 3 events at 10:xx, 2 events at 11:xx
	records := []Record{
		rec("s1", "a", apmconfig.EventAgentSpawn, "", 0),                    // 10:00
		rec("s1", "a", apmconfig.EventUserPromptSubmit, "", 10*time.Minute), // 10:10
		rec("s1", "a", apmconfig.EventPreToolUse, "bash", 20*time.Minute),   // 10:20
		rec("s1", "a", apmconfig.EventPostToolUse, "bash", 70*time.Minute),  // 11:10
		rec("s1", "a", apmconfig.EventStop, "", 80*time.Minute),             // 11:20
	}

	m := aggregateOverview(records, now)

	byHour := map[time.Time]int{}
	for _, h := range m.HourlyActivity {
		byHour[h.Hour] = h.EventCount
	}

	hour10 := baseTime.Truncate(time.Hour) // 10:00
	hour11 := hour10.Add(time.Hour)        // 11:00

	if byHour[hour10] != 3 {
		t.Errorf("hour 10: want 3 events, got %d", byHour[hour10])
	}
	if byHour[hour11] != 2 {
		t.Errorf("hour 11: want 2 events, got %d", byHour[hour11])
	}
}

func TestAggregateDetail(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(30 * time.Minute)
	records := []Record{
		{Ts: baseTime, Session: "s1", Agent: "coder", Event: apmconfig.EventAgentSpawn, Cwd: "/tmp"},
		{Ts: baseTime.Add(1 * time.Minute), Session: "s1", Agent: "coder", Event: apmconfig.EventUserPromptSubmit, Prompt: "first"},
		{Ts: baseTime.Add(2 * time.Minute), Session: "s1", Agent: "coder", Event: apmconfig.EventUserPromptSubmit, Prompt: "second"},
		{Ts: baseTime.Add(3 * time.Minute), Session: "s1", Agent: "coder", Event: apmconfig.EventPreToolUse, Tool: "bash"},
		{Ts: baseTime.Add(3*time.Minute + 2*time.Second), Session: "s1", Agent: "coder", Event: apmconfig.EventPostToolUse, Tool: "bash"},
		{Ts: baseTime.Add(4 * time.Minute), Session: "s1", Agent: "coder", Event: apmconfig.EventPreToolUse, Tool: "grep"}, // unmatched => error
	}

	d := mustAggregate(t, records, now)

	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session detail, got %d", len(d.Sessions))
	}
	sd := d.Sessions[0]
	if sd.Cwd != "/tmp" {
		t.Errorf("cwd: want /tmp, got %q", sd.Cwd)
	}
	if len(sd.PromptHistory) != 2 || sd.PromptHistory[0] != "second" {
		t.Errorf("prompts newest-first expected, got %v", sd.PromptHistory)
	}
	// Timeline: 6 events, last one is the unmatched preToolUse -> IsError true
	if len(sd.Timeline) != 6 {
		t.Fatalf("timeline: want 6 events, got %d", len(sd.Timeline))
	}
	if !sd.Timeline[5].IsError {
		t.Errorf("expected unmatched preToolUse to be flagged as error on timeline")
	}

	// Tool details
	var bash, grep ToolDetail
	for _, td := range d.Tools {
		switch td.Name {
		case "bash":
			bash = td
		case "grep":
			grep = td
		}
	}
	if bash.AvgDuration != JSONDuration(2*time.Second) {
		t.Errorf("bash avg duration: want 2s, got %v", bash.AvgDuration)
	}
	if len(bash.RecentCalls) != 1 {
		t.Errorf("bash recent calls: want 1, got %d", len(bash.RecentCalls))
	}
	if grep.ErrorCount != 1 || len(grep.Errors) != 1 {
		t.Errorf("grep should have 1 error sample, got ErrorCount=%d len(Errors)=%d", grep.ErrorCount, len(grep.Errors))
	}

	// Agent detail
	if len(d.Agents) != 1 {
		t.Fatalf("want 1 agent detail, got %d", len(d.Agents))
	}
	a := d.Agents[0]
	if a.ToolErrorCnt != 1 {
		t.Errorf("agent ToolErrorCnt: want 1, got %d", a.ToolErrorCnt)
	}
	toolMap := map[string]SessionToolSummary{}
	for _, ts := range a.ToolSummary {
		toolMap[ts.Tool] = ts
	}
	if toolMap["bash"].CallCount != 1 || toolMap["grep"].CallCount != 1 {
		t.Errorf("agent ToolSummary wrong: %v", a.ToolSummary)
	}
}

func TestAggregateDetailSameTimestampKeepsPreBeforePost(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(30 * time.Minute)
	toolInput := json.RawMessage(`{"command":"echo hi"}`)
	records := []Record{
		{Ts: baseTime, Session: "s1", Agent: "coder", Event: apmconfig.EventPreToolUse, Tool: "bash", ToolInput: toolInput},
		{Ts: baseTime, Session: "s1", Agent: "coder", Event: apmconfig.EventPostToolUse, Tool: "bash", ToolInput: toolInput},
	}

	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session detail, got %d", len(d.Sessions))
	}
	sd := d.Sessions[0]
	if len(sd.Timeline) != 2 {
		t.Fatalf("timeline: want 2 events, got %d", len(sd.Timeline))
	}
	if sd.Timeline[0].Event != apmconfig.EventPreToolUse || sd.Timeline[1].Event != apmconfig.EventPostToolUse {
		t.Fatalf("timeline order: want pre/post, got %q/%q", sd.Timeline[0].Event, sd.Timeline[1].Event)
	}
	if sd.Timeline[0].IsError {
		t.Fatalf("preToolUse should stay matched, got IsError=true")
	}

	var bash ToolDetail
	for _, td := range d.Tools {
		if td.Name == "bash" {
			bash = td
		}
	}
	if bash.CallCount != 1 {
		t.Fatalf("CallCount: want 1, got %d", bash.CallCount)
	}
	if bash.ErrorCount != 0 {
		t.Fatalf("ErrorCount: want 0, got %d", bash.ErrorCount)
	}
	if len(bash.Errors) != 0 {
		t.Fatalf("Errors: want 0, got %d", len(bash.Errors))
	}
	if len(bash.RecentCalls) != 1 {
		t.Fatalf("RecentCalls: want 1, got %d", len(bash.RecentCalls))
	}
}

// --- New-feature tests ------------------------------------------------------

func TestAggregateLastActivitySort(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(2 * time.Hour)
	records := []Record{
		// s1: starts earliest, last activity at +10min (not active)
		rec("s1", "a", apmconfig.EventAgentSpawn, "", 0),
		rec("s1", "a", apmconfig.EventStop, "", 10*time.Minute),
		// s2: starts later, last activity at +1h (not active)
		rec("s2", "a", apmconfig.EventAgentSpawn, "", 30*time.Minute),
		rec("s2", "a", apmconfig.EventStop, "", 60*time.Minute),
		// s3: starts in middle, last activity at +90min (newest)
		rec("s3", "a", apmconfig.EventAgentSpawn, "", 20*time.Minute),
		rec("s3", "a", apmconfig.EventStop, "", 90*time.Minute),
	}
	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 3 {
		t.Fatalf("want 3 sessions, got %d", len(d.Sessions))
	}
	wantOrder := []string{"s3", "s2", "s1"}
	for i, id := range wantOrder {
		if d.Sessions[i].ID != id {
			t.Errorf("sessions[%d]: want %s, got %s (full order: %v)", i, id, d.Sessions[i].ID, sessionIDs(d.Sessions))
		}
	}
	// LastActivity populated
	if d.Sessions[0].LastActivity != baseTime.Add(90*time.Minute) {
		t.Errorf("LastActivity wrong: got %v", d.Sessions[0].LastActivity)
	}
}

func sessionIDs(ss []SessionDetail) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.ID
	}
	return out
}

func TestInputSummaryExtraction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", ``, ""},
		{"null", `null`, ""},
		{"actual input wins over purpose", `{"__tool_use_purpose":"read the plan","operations":[{"path":"a"}]}`, "a"},
		{"purpose fallback when no input fields", `{"__tool_use_purpose":"read the plan"}`, "read the plan"},
		{"command field", `{"command":"go test ./..."}`, "go test ./..."},
		{"path field", `{"path":"/tmp/foo"}`, "/tmp/foo"},
		{"pattern field", `{"pattern":"foo.*bar"}`, "foo.*bar"},
		{"newlines replaced", `{"__tool_use_purpose":"a\nb\tc"}`, "a b c"},
		{"truncates", `{"__tool_use_purpose":"` + strings.Repeat("x", 200) + `"}`, strings.Repeat("x", 119) + "…"},
		{"fallback first string", `{"zzz":"last","aaa":"first"}`, "first"},
		{"unparseable", `not-json`, "not-json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := inputSummary([]byte(c.raw), "", "")
			if got != c.want {
				t.Errorf("inputSummary(%q) = %q; want %q", c.raw, got, c.want)
			}
		})
	}
}

func TestInputSummaryToolSpecific(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		tool string
		raw  string
		cwd  string
		want string
	}{
		{"read with offset+limit", "read", `{"operations":[{"mode":"Line","path":"/a/b.go","offset":100,"limit":80}]}`, "", "/a/b.go:100-180"},
		{"read with limit only", "read", `{"operations":[{"path":"/a.go","limit":50}]}`, "", "/a.go:1-51"},
		{"read path only", "read", `{"operations":[{"path":"/a.go"}]}`, "", "/a.go"},
		{"read multiple ops", "read", `{"operations":[{"path":"/a.go"},{"path":"/b.go"}]}`, "", "/a.go (+1 more)"},
		{"read image", "read", `{"operations":[{"mode":"Image","image_paths":["/p.png"]}]}`, "", "/p.png"},
		{"grep with path", "grep", `{"pattern":"foo","path":"/src"}`, "", `"foo" in /src`},
		{"grep pattern only", "grep", `{"pattern":"foo"}`, "", `"foo"`},
		{"glob with path", "glob", `{"pattern":"**/*.go","path":"/src"}`, "", "**/*.go in /src"},
		{"glob pattern only", "glob", `{"pattern":"**/*.go"}`, "", "**/*.go"},
		{"shell strips cd to cwd with &&", "shell", `{"command":"cd /ws && go test ./..."}`, "/ws", "go test ./..."},
		{"shell strips cd to cwd with ;", "shell", `{"command":"cd /ws; ls"}`, "/ws", "ls"},
		{"shell keeps cd to different dir", "shell", `{"command":"cd /other && ls"}`, "/ws", "cd /other && ls"},
		{"shell no cd prefix", "shell", `{"command":"echo hi"}`, "/ws", "echo hi"},
		{"shell no cwd info", "shell", `{"command":"cd /ws && ls"}`, "", "cd /ws && ls"},
		{"shell bare cd only", "shell", `{"command":"cd /ws"}`, "/ws", ""},
		{"write with command", "write", `{"command":"strReplace","path":"/a.go","oldStr":"x","newStr":"y"}`, "", "strReplace /a.go"},
		{"write create", "write", `{"command":"create","path":"/new.md","content":"hi"}`, "", "create /new.md"},
		{"write path only", "write", `{"path":"/a.go"}`, "", "/a.go"},
		{"unknown tool falls back", "other", `{"path":"/x"}`, "", "/x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := inputSummary([]byte(c.raw), c.tool, c.cwd)
			if got != c.want {
				t.Errorf("inputSummary(%q, %q, %q) = %q; want %q", c.raw, c.tool, c.cwd, got, c.want)
			}
		})
	}
}

func TestUnknownToolFallthrough(t *testing.T) {
	t.Parallel()
	// An unregistered tool name must fall through to genericSummary and return a non-empty result.
	got := inputSummary([]byte(`{"path":"/some/file.go"}`), "no-such-tool", "")
	if got == "" {
		t.Error("expected non-empty summary for unknown tool, got empty string")
	}
	if got != "/some/file.go" {
		t.Errorf("got %q; want %q", got, "/some/file.go")
	}
}

func TestAggregateInputSummaryPropagates(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(time.Hour)
	records := []Record{
		{
			Ts: baseTime, Session: "s", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "read",
			ToolInput: []byte(`{"__tool_use_purpose":"read plan"}`),
		},
		{
			Ts: baseTime.Add(time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "read",
		},
		// Second call: unmatched → error, with a different summary source.
		{
			Ts: baseTime.Add(2 * time.Minute), Session: "s", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "read",
			ToolInput: []byte(`{"operations":[{"path":"/x/y/SKILL.md"}]}`),
		},
	}
	d := mustAggregate(t, records, now)
	var td ToolDetail
	for _, t2 := range d.Tools {
		if t2.Name == "read" {
			td = t2
		}
	}
	if len(td.RecentCalls) != 1 || td.RecentCalls[0].InputSummary != "read plan" {
		t.Errorf("RecentCalls[0].InputSummary = %q; want %q", summariesOf(td.RecentCalls), "read plan")
	}
	if len(td.Errors) != 1 || td.Errors[0].InputSummary == "" {
		t.Errorf("Errors[0].InputSummary should not be empty, got %v", summariesOf(td.Errors))
	}
}

func summariesOf(cs []ToolCall) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.InputSummary
	}
	return out
}

func TestAggregateSkillsCounting(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(time.Hour)
	// two reads of task-verification/SKILL.md, one of git-master/SKILL.md, one unrelated
	records := []Record{
		{Ts: baseTime, Session: "s", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "read",
			ToolInput: []byte(`{"operations":[{"path":".kiro/skills/task-verification/SKILL.md"}]}`)},
		{Ts: baseTime.Add(1 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "read"},
		{Ts: baseTime.Add(2 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "read",
			ToolInput: []byte(`{"operations":[{"path":"/abs/.kiro/skills/task-verification/SKILL.md"}]}`)},
		{Ts: baseTime.Add(3 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "read"},
		{Ts: baseTime.Add(4 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "read",
			ToolInput: []byte(`{"operations":[{"path":".kiro/skills/git-master/SKILL.md"}]}`)},
		{Ts: baseTime.Add(5 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "read"},
		// unrelated: read of a non-SKILL.md path
		{Ts: baseTime.Add(6 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "read",
			ToolInput: []byte(`{"operations":[{"path":"/tmp/foo.go"}]}`)},
		{Ts: baseTime.Add(7 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "read"},
		// tool != "read" should not count
		{Ts: baseTime.Add(8 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "shell",
			ToolInput: []byte(`{"command":"cat code-search/SKILL.md"}`)},
		{Ts: baseTime.Add(9 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "shell"},
	}
	d := mustAggregate(t, records, now)
	if len(d.Skills) != 2 {
		t.Fatalf("want 2 skills, got %d (%v)", len(d.Skills), d.Skills)
	}
	if d.Skills[0].Name != "task-verification" || d.Skills[0].ReadCount != 2 {
		t.Errorf("Skills[0] = %+v; want task-verification/2", d.Skills[0])
	}
	if d.Skills[1].Name != "git-master" || d.Skills[1].ReadCount != 1 {
		t.Errorf("Skills[1] = %+v; want git-master/1", d.Skills[1])
	}
}

func TestAggregateEventEntryInputSummary(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(time.Hour)
	records := []Record{
		{
			Ts: baseTime, Session: "s", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"echo hello"}`),
		},
		{
			Ts: baseTime.Add(time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "bash",
		},
		{
			Ts: baseTime.Add(2 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventUserPromptSubmit,
		},
	}
	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(d.Sessions))
	}
	tl := d.Sessions[0].Timeline
	// preToolUse entry should have InputSummary set
	var pre, post, prompt *EventEntry
	for i := range tl {
		switch tl[i].Event {
		case apmconfig.EventPreToolUse:
			pre = &tl[i]
		case apmconfig.EventPostToolUse:
			post = &tl[i]
		case apmconfig.EventUserPromptSubmit:
			prompt = &tl[i]
		}
	}
	if pre == nil {
		t.Fatal("preToolUse entry not found in timeline")
	}
	if pre.InputSummary != "echo hello" {
		t.Errorf("preToolUse InputSummary = %q; want %q", pre.InputSummary, "echo hello")
	}
	if post != nil && post.InputSummary != "" {
		t.Errorf("postToolUse InputSummary should be empty, got %q", post.InputSummary)
	}
	if prompt != nil && prompt.InputSummary != "" {
		t.Errorf("userPromptSubmit InputSummary should be empty, got %q", prompt.InputSummary)
	}
}

func TestAggregateSkillsNoMatches(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(time.Hour)
	records := []Record{
		{Ts: baseTime, Session: "s", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "read",
			ToolInput: []byte(`{"operations":[{"path":"/tmp/foo.go"}]}`)},
		{Ts: baseTime.Add(1 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "read"},
	}
	d := mustAggregate(t, records, now)
	if len(d.Skills) != 0 {
		t.Errorf("want 0 skills, got %+v", d.Skills)
	}
}

func TestAggregateDetailCap(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(24 * time.Hour)
	var records []Record
	// 150 matched calls for "bash" (pre+post pairs), timestamps spread 1s apart
	for i := 0; i < 150; i++ {
		offset := time.Duration(i) * time.Second
		records = append(records,
			rec("s1", "a", apmconfig.EventPreToolUse, "bash", offset),
			rec("s1", "a", apmconfig.EventPostToolUse, "bash", offset+500*time.Millisecond),
		)
	}
	// 75 unmatched preToolUse for "grep" → errors
	for i := 0; i < 75; i++ {
		offset := time.Duration(i) * time.Second
		records = append(records, rec("s1", "a", apmconfig.EventPreToolUse, "grep", offset))
	}

	d := mustAggregate(t, records, now)

	var bash, grep ToolDetail
	for _, td := range d.Tools {
		switch td.Name {
		case "bash":
			bash = td
		case "grep":
			grep = td
		}
	}

	if len(bash.RecentCalls) != maxRecentCalls {
		t.Errorf("RecentCalls: want %d, got %d", maxRecentCalls, len(bash.RecentCalls))
	}
	// newest first: index 0 should have the latest timestamp (offset 149s)
	wantNewest := baseTime.Add(149 * time.Second)
	if !bash.RecentCalls[0].Ts.Equal(wantNewest) {
		t.Errorf("RecentCalls[0].Ts: want %v, got %v", wantNewest, bash.RecentCalls[0].Ts)
	}

	if len(grep.Errors) != maxErrors {
		t.Errorf("Errors: want %d, got %d", maxErrors, len(grep.Errors))
	}
	// newest first: index 0 should have the latest timestamp (offset 74s)
	wantNewestErr := baseTime.Add(74 * time.Second)
	if !grep.Errors[0].Ts.Equal(wantNewestErr) {
		t.Errorf("Errors[0].Ts: want %v, got %v", wantNewestErr, grep.Errors[0].Ts)
	}
}

// Regression: for tools dispatching sub-operations (like "code" with different
// LSP operations), a slow op's pending must not be popped by a faster op's
// postToolUse. All three calls here complete, so ErrorCount must be 0.
func TestAggregateCodeOperationsDoNotCrossMatch(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(time.Hour)
	records := []Record{
		// Slow get_diagnostics starts first but finishes last.
		{Ts: baseTime, Session: "s", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "code",
			ToolInput: []byte(`{"operation":"get_diagnostics"}`)},
		// Fast get_hover starts + finishes while get_diagnostics is pending.
		{Ts: baseTime.Add(1 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "code",
			ToolInput: []byte(`{"operation":"get_hover"}`)},
		{Ts: baseTime.Add(2 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "code",
			ToolInput: []byte(`{"operation":"get_hover"}`)},
		// get_diagnostics finally completes.
		{Ts: baseTime.Add(10 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "code",
			ToolInput: []byte(`{"operation":"get_diagnostics"}`)},
	}
	d := mustAggregate(t, records, now)
	var code ToolDetail
	for _, t2 := range d.Tools {
		if t2.Name == "code" {
			code = t2
		}
	}
	if code.CallCount != 2 {
		t.Fatalf("CallCount: want 2, got %d", code.CallCount)
	}
	if code.ErrorCount != 0 {
		t.Errorf("ErrorCount: want 0 (both calls completed), got %d", code.ErrorCount)
	}
	if len(code.RecentCalls) != 2 {
		t.Errorf("RecentCalls: want 2, got %d", len(code.RecentCalls))
	}
}

// Regression: shell1 start, shell2 start, shell2 end, shell1 end.
// With the old FIFO-by-tool-name logic, shell1's duration would be
// (shell2 end - shell1 start), shuffling times between calls. Matching by
// tool_input keeps each call paired with its own post.
func TestAggregateShellConcurrentOutOfOrderDurations(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(time.Hour)
	records := []Record{
		{Ts: baseTime, Session: "s", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "shell",
			ToolInput: []byte(`{"command":"sleep 10"}`)},
		{Ts: baseTime.Add(1 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "shell",
			ToolInput: []byte(`{"command":"echo fast"}`)},
		{Ts: baseTime.Add(2 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "shell",
			ToolInput: []byte(`{"command":"echo fast"}`)},
		{Ts: baseTime.Add(10 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "shell",
			ToolInput: []byte(`{"command":"sleep 10"}`)},
	}
	d := mustAggregate(t, records, now)
	byName := map[string]ToolDetail{}
	for _, td := range d.Tools {
		byName[td.Name] = td
	}
	sleep := byName["shell:sleep"]
	echo := byName["shell:echo"]
	if sleep.CallCount != 1 || sleep.ErrorCount != 0 {
		t.Fatalf("shell:sleep: want 1/0, got %d/%d", sleep.CallCount, sleep.ErrorCount)
	}
	if echo.CallCount != 1 || echo.ErrorCount != 0 {
		t.Fatalf("shell:echo: want 1/0, got %d/%d", echo.CallCount, echo.ErrorCount)
	}
	// Durations must pair correctly: sleep=10s, echo fast=1s.
	if d := time.Duration(sleep.RecentCalls[0].Duration); d != 10*time.Second {
		t.Errorf("sleep 10 duration: want 10s, got %v", d)
	}
	if d := time.Duration(echo.RecentCalls[0].Duration); d != 1*time.Second {
		t.Errorf("echo fast duration: want 1s, got %v", d)
	}
}

func TestFirst(t *testing.T) {
	t.Parallel()
	// Empty slice
	var empty []int
	val, ok := first(empty)
	if ok || val != 0 {
		t.Errorf("first(empty): want (0, false), got (%d, %v)", val, ok)
	}

	// Single element
	single := []int{42}
	val, ok = first(single)
	if !ok || val != 42 {
		t.Errorf("first(single): want (42, true), got (%d, %v)", val, ok)
	}

	// Multiple elements
	multi := []int{10, 20, 30}
	val, ok = first(multi)
	if !ok || val != 10 {
		t.Errorf("first(multi): want (10, true), got (%d, %v)", val, ok)
	}

	// String slice
	strs := []string{"hello", "world"}
	str, ok := first(strs)
	if !ok || str != "hello" {
		t.Errorf("first(strs): want (hello, true), got (%s, %v)", str, ok)
	}

	// Empty string slice
	var emptyStrs []string
	str, ok = first(emptyStrs)
	if ok || str != "" {
		t.Errorf("first(emptyStrs): want (\"\", false), got (%s, %v)", str, ok)
	}
}

func BenchmarkPendingKey(b *testing.B) {
	input := json.RawMessage(`{"command":"go test ./...","working_dir":"/home/user/project"}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = pendingKey("shell", input)
	}
}

func BenchmarkAggregate(b *testing.B) {
	dir := filepath.Join("..", "..", "testdata", "monitor")
	recs, err := LoadRecords(dir, time.Time{})
	if err != nil {
		b.Fatalf("LoadRecords: %v", err)
	}
	if len(recs) == 0 {
		b.Fatal("no records loaded from testdata")
	}
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := AggregateDetail(context.Background(), recs, now)
		if err != nil {
			b.Fatalf("AggregateDetail: %v", err)
		}
	}
}

func TestToolInput_IgnoresUnknown(t *testing.T) {
	t.Parallel()
	// Unknown fields in tool_input are silently ignored; known fields decode.
	raw := []byte(`{"path":"a","unknown_field":42,"another":{"nested":true}}`)
	var in toolInput
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if in.Path != "a" {
		t.Errorf("Path = %q; want %q", in.Path, "a")
	}
}

// TestMixedAgentFixture loads the mixed-agent.jsonl fixture and verifies that
// two agents under the same session ID produce two distinct SessionDetails with
// correct AgentKey and Title fields.
func TestMixedAgentFixture(t *testing.T) {
	t.Parallel()
	logsDir := filepath.Join("..", "..", "testdata", "monitor", "logs")
	recs, err := LoadRecords(logsDir, time.Time{})
	if err != nil {
		t.Fatalf("LoadRecords: %v", err)
	}
	// Filter to only the mixed-agent session.
	var mixed []Record
	for _, r := range recs {
		if r.Session == "ma-session-001" {
			mixed = append(mixed, r)
		}
	}
	if len(mixed) == 0 {
		t.Fatal("no records found for ma-session-001")
	}

	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	d := mustAggregate(t, mixed, now)

	if len(d.Sessions) != 2 {
		t.Fatalf("want 2 SessionDetails for ma-session-001, got %d", len(d.Sessions))
	}
	byAgent := map[string]SessionDetail{}
	for _, sd := range d.Sessions {
		if sd.ID != "ma-session-001" {
			t.Errorf("unexpected session ID %q", sd.ID)
		}
		byAgent[sd.Agent] = sd
	}

	orc, ok := byAgent["orchestrator"]
	if !ok {
		t.Fatal("missing orchestrator SessionDetail")
	}
	lead, ok := byAgent["lead"]
	if !ok {
		t.Fatal("missing lead SessionDetail")
	}

	if orc.AgentKey != "ma-session-001|orchestrator" {
		t.Errorf("orc.AgentKey: want %q, got %q", "ma-session-001|orchestrator", orc.AgentKey)
	}
	if lead.AgentKey != "ma-session-001|lead" {
		t.Errorf("lead.AgentKey: want %q, got %q", "ma-session-001|lead", lead.AgentKey)
	}
	if orc.Title != "orchestrate the build pipeline" {
		t.Errorf("orc.Title: want %q, got %q", "orchestrate the build pipeline", orc.Title)
	}
	if lead.Title != "implement the assigned subtask" {
		t.Errorf("lead.Title: want %q, got %q", "implement the assigned subtask", lead.Title)
	}
	// orchestrator: 3 matched tool calls (shell, read, shell) + 1 unmatched grep
	if orc.ToolCalls != 4 {
		t.Errorf("orc.ToolCalls: want 4, got %d", orc.ToolCalls)
	}
	// lead: 2 matched tool calls (read, shell)
	if lead.ToolCalls != 2 {
		t.Errorf("lead.ToolCalls: want 2, got %d", lead.ToolCalls)
	}
}

// TestAggregateDetailGolden loads real fixture sessions from testdata/monitor
// and compares the full AggregateDetail output to a golden snapshot. Set
// UPDATE_GOLDEN=1 to rewrite the golden file.
func TestAggregateDetailGolden(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("..", "..", "testdata", "monitor")
	recs, err := LoadRecords(dir, time.Time{})
	if err != nil {
		t.Fatalf("LoadRecords: %v", err)
	}
	// Fixed "now" so Active computation is deterministic.
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	dm := mustAggregate(t, recs, now)
	got, err := json.MarshalIndent(dm, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = normalizeGoldenBytes(got)
	goldenPath := filepath.Join(dir, "aggregate.golden.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, append(got, '\n'), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with UPDATE_GOLDEN=1 to create): %v", err)
	}
	want = normalizeGoldenBytes(want)
	if string(got) != string(want) {
		t.Errorf("AggregateDetail golden mismatch.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestNormalizeGoldenBytes(t *testing.T) {
	t.Parallel()
	got := string(normalizeGoldenBytes([]byte("{\r\n  \"ok\": true\r\n}\r\n")))
	if got != "{\n  \"ok\": true\n}" {
		t.Fatalf("normalizeGoldenBytes() = %q", got)
	}
}

func normalizeGoldenBytes(b []byte) []byte {
	return []byte(strings.TrimRight(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n"))
}

// --- AggregateToolsFromTimeline ---------------------------------------------

func TestAggregateToolsFromTimeline(t *testing.T) {
	t.Parallel()
	// Build two sessions across two agents with a mix of matched and
	// unmatched (error) preToolUse events. We feed the fully-aggregated
	// SessionDetails back into AggregateToolsFromTimeline and verify the
	// tool-level counts and ToolCall attribution.
	now := baseTime.Add(1 * time.Hour)
	records := []Record{
		// s1/agentA: read x2 (both matched), bash x1 (unmatched → error)
		rec("s1", "agentA", apmconfig.EventPreToolUse, "read", 0),
		rec("s1", "agentA", apmconfig.EventPostToolUse, "read", 1*time.Minute),
		rec("s1", "agentA", apmconfig.EventPreToolUse, "read", 2*time.Minute),
		rec("s1", "agentA", apmconfig.EventPostToolUse, "read", 3*time.Minute),
		rec("s1", "agentA", apmconfig.EventPreToolUse, "bash", 4*time.Minute),
		// s2/agentB: read x1 (matched)
		rec("s2", "agentB", apmconfig.EventPreToolUse, "read", 10*time.Minute),
		rec("s2", "agentB", apmconfig.EventPostToolUse, "read", 11*time.Minute),
	}
	d := mustAggregate(t, records, now)

	details, metrics := AggregateToolsFromTimeline(d.Sessions)

	byName := map[string]ToolDetail{}
	for _, td := range details {
		byName[td.Name] = td
	}

	read := byName["read"]
	if read.CallCount != 3 {
		t.Errorf("read CallCount: want 3, got %d", read.CallCount)
	}
	if read.ErrorCount != 0 {
		t.Errorf("read ErrorCount: want 0, got %d", read.ErrorCount)
	}
	if read.ErrorRate != 0 {
		t.Errorf("read ErrorRate: want 0, got %f", read.ErrorRate)
	}
	if len(read.RecentCalls) != 3 {
		t.Errorf("read RecentCalls: want 3, got %d", len(read.RecentCalls))
	}
	// Verify Session/Agent attribution on each ToolCall.
	sessions := map[string]bool{}
	agents := map[string]bool{}
	for _, c := range read.RecentCalls {
		sessions[c.Session] = true
		agents[c.Agent] = true
		if c.Tool != "read" {
			t.Errorf("ToolCall.Tool: want read, got %q", c.Tool)
		}
	}
	if !sessions["s1"] || !sessions["s2"] {
		t.Errorf("expected ToolCall sessions s1,s2 got %v", sessions)
	}
	if !agents["agentA"] || !agents["agentB"] {
		t.Errorf("expected ToolCall agents agentA,agentB got %v", agents)
	}

	bash := byName["bash"]
	if bash.CallCount != 1 {
		t.Errorf("bash CallCount: want 1, got %d", bash.CallCount)
	}
	if bash.ErrorCount != 1 {
		t.Errorf("bash ErrorCount: want 1, got %d", bash.ErrorCount)
	}
	if bash.ErrorRate != 1.0 {
		t.Errorf("bash ErrorRate: want 1.0, got %f", bash.ErrorRate)
	}
	if len(bash.Errors) != 1 {
		t.Errorf("bash Errors: want 1, got %d", len(bash.Errors))
	}
	if bash.Errors[0].Session != "s1" || bash.Errors[0].Agent != "agentA" {
		t.Errorf("bash error attribution: want s1/agentA, got %s/%s",
			bash.Errors[0].Session, bash.Errors[0].Agent)
	}

	// Overview metrics mirror the details.
	mByName := map[string]ToolMetric{}
	for _, tm := range metrics {
		mByName[tm.Name] = tm
	}
	if mByName["read"].CallCount != 3 || mByName["bash"].CallCount != 1 {
		t.Errorf("metrics mismatch: %+v", mByName)
	}
}

func TestAggregateToolsFromTimelineEmpty(t *testing.T) {
	t.Parallel()
	details, metrics := AggregateToolsFromTimeline(nil)
	if len(details) != 0 || len(metrics) != 0 {
		t.Errorf("want empty slices, got %d details / %d metrics", len(details), len(metrics))
	}
}

// --- (sid, agent) aggregation (task-3) --------------------------------------

func TestCompositeKeyFormat(t *testing.T) {
	t.Parallel()
	if got, want := compositeKey("sidX", "agentY"), "sidX|agentY"; got != want {
		t.Errorf("compositeKey: got %q, want %q", got, want)
	}
	if got, want := compositeKey("", ""), "|"; got != want {
		t.Errorf("compositeKey empty: got %q, want %q", got, want)
	}
}

func TestAggregateMultiAgentSameSid(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []Record{
		// Same sid "s1" under two agents — must yield two SessionDetails.
		{Ts: baseTime, Session: "s1", Agent: "orchestrator", Event: apmconfig.EventUserPromptSubmit, Prompt: "orc prompt"},
		{Ts: baseTime.Add(1 * time.Minute), Session: "s1", Agent: "orchestrator", Event: apmconfig.EventPreToolUse, Tool: "bash"},
		{Ts: baseTime.Add(2 * time.Minute), Session: "s1", Agent: "orchestrator", Event: apmconfig.EventPostToolUse, Tool: "bash"},
		{Ts: baseTime.Add(3 * time.Minute), Session: "s1", Agent: "lead", Event: apmconfig.EventUserPromptSubmit, Prompt: "lead prompt"},
		{Ts: baseTime.Add(4 * time.Minute), Session: "s1", Agent: "lead", Event: apmconfig.EventPreToolUse, Tool: "grep"},
		{Ts: baseTime.Add(5 * time.Minute), Session: "s1", Agent: "lead", Event: apmconfig.EventPostToolUse, Tool: "grep"},
	}
	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 2 {
		t.Fatalf("want 2 SessionDetails, got %d", len(d.Sessions))
	}
	byAgent := map[string]SessionDetail{}
	for _, sd := range d.Sessions {
		if sd.ID != "s1" {
			t.Errorf("ID: want s1, got %q", sd.ID)
		}
		byAgent[sd.Agent] = sd
	}
	orc, ok := byAgent["orchestrator"]
	if !ok {
		t.Fatalf("missing orchestrator SessionDetail")
	}
	lead, ok := byAgent["lead"]
	if !ok {
		t.Fatalf("missing lead SessionDetail")
	}
	if orc.AgentKey == lead.AgentKey {
		t.Errorf("AgentKeys must differ, both got %q", orc.AgentKey)
	}
	if orc.AgentKey != "s1|orchestrator" {
		t.Errorf("orc.AgentKey: want %q, got %q", "s1|orchestrator", orc.AgentKey)
	}
	if lead.AgentKey != "s1|lead" {
		t.Errorf("lead.AgentKey: want %q, got %q", "s1|lead", lead.AgentKey)
	}
	if orc.Title != "orc prompt" {
		t.Errorf("orc.Title: want %q, got %q", "orc prompt", orc.Title)
	}
	if lead.Title != "lead prompt" {
		t.Errorf("lead.Title: want %q, got %q", "lead prompt", lead.Title)
	}
	if orc.ToolCalls != 1 || lead.ToolCalls != 1 {
		t.Errorf("ToolCalls split by agent: orc=%d lead=%d (want 1/1)", orc.ToolCalls, lead.ToolCalls)
	}
}

func TestAggregateSummaryToolTitlePreferred(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []Record{
		{Ts: baseTime, Session: "s1", Agent: "coder", Event: apmconfig.EventUserPromptSubmit, Prompt: "first prompt"},
		{Ts: baseTime.Add(1 * time.Minute), Session: "s1", Agent: "coder", Event: apmconfig.EventPreToolUse, Tool: "summary",
			ToolInput: []byte(`{"taskDescription":"implement feature X"}`)},
		{Ts: baseTime.Add(2 * time.Minute), Session: "s1", Agent: "coder", Event: apmconfig.EventPostToolUse, Tool: "summary"},
	}
	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 SessionDetail, got %d", len(d.Sessions))
	}
	if got, want := d.Sessions[0].Title, "implement feature X"; got != want {
		t.Errorf("Title: want %q, got %q", want, got)
	}
}

func TestAggregateEmptyAgentNormalized(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []Record{
		{Ts: baseTime, Session: "s1", Agent: "", Event: apmconfig.EventUserPromptSubmit, Prompt: "hi"},
		{Ts: baseTime.Add(1 * time.Minute), Session: "s1", Agent: "", Event: apmconfig.EventPreToolUse, Tool: "bash"},
		{Ts: baseTime.Add(2 * time.Minute), Session: "s1", Agent: "", Event: apmconfig.EventPostToolUse, Tool: "bash"},
	}
	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 SessionDetail, got %d", len(d.Sessions))
	}
	sd := d.Sessions[0]
	if sd.Agent != "(unknown)" {
		t.Errorf("Agent: want %q, got %q", "(unknown)", sd.Agent)
	}
	if sd.AgentKey != "s1|(unknown)" {
		t.Errorf("AgentKey: want %q, got %q", "s1|(unknown)", sd.AgentKey)
	}
	if sd.Title != "hi" {
		t.Errorf("Title: want %q, got %q", "hi", sd.Title)
	}
}

func TestAggregateSessionMetricAgentKeyFormat(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(10 * time.Minute)
	records := []Record{
		rec("sX", "aY", apmconfig.EventAgentSpawn, "", 0),
	}
	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(d.Sessions))
	}
	sd := d.Sessions[0]
	if want := sd.ID + "|" + sd.Agent; sd.AgentKey != want {
		t.Errorf("AgentKey: want %q, got %q", want, sd.AgentKey)
	}
}

// --- Task 1: tool_response exit_status error detection ----------------------

func TestParseToolResponseError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"exit status 0 is not error", `{"items":[{"Json":{"exit_status":"exit status: 0"}}]}`, false},
		{"exit status 1 is error", `{"items":[{"Json":{"exit_status":"exit status: 1"}}]}`, true},
		{"exit status 127 is error", `{"items":[{"Json":{"exit_status":"exit status: 127"}}]}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := parseToolResponseError(json.RawMessage(c.raw))
			if got != c.want {
				t.Errorf("parseToolResponseError(%q) = %v; want %v", c.raw, got, c.want)
			}
		})
	}
}

func TestParseToolResponseError_EdgeCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  []byte
		want bool
	}{
		{"nil", nil, false},
		{"empty", []byte(""), false},
		{"null", []byte("null"), false},
		{"empty items", []byte(`{"items":[]}`), false},
		{"text item no Json", []byte(`{"items":[{"Text":"file contents..."}]}`), false},
		{"Json without exit_status", []byte(`{"items":[{"Json":{}}]}`), false},
		{"exit_status bad prefix", []byte(`{"items":[{"Json":{"exit_status":"rubbish"}}]}`), false},
		{"exit_status Atoi fail", []byte(`{"items":[{"Json":{"exit_status":"exit status: abc"}}]}`), false},
		{"exit_status 000 is zero", []byte(`{"items":[{"Json":{"exit_status":"exit status: 000"}}]}`), false},
		{"exit_status -1 is error", []byte(`{"items":[{"Json":{"exit_status":"exit status: -1"}}]}`), true},
		{"exit_status 255 is error", []byte(`{"items":[{"Json":{"exit_status":"exit status: 255"}}]}`), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := parseToolResponseError(json.RawMessage(c.raw))
			if got != c.want {
				t.Errorf("parseToolResponseError(%q) = %v; want %v", c.raw, got, c.want)
			}
		})
	}
}

func TestAggregateDetailExitStatusError(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(30 * time.Minute)
	exitStatus1 := json.RawMessage(`{"items":[{"Json":{"exit_status":"exit status: 1"}}]}`)
	exitStatus0 := json.RawMessage(`{"items":[{"Json":{"exit_status":"exit status: 0"}}]}`)
	records := []Record{
		// bash call 1: exit status 1 → error
		{Ts: baseTime, Session: "s1", Agent: "coder", Event: apmconfig.EventPreToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"false"}`)},
		{Ts: baseTime.Add(1 * time.Second), Session: "s1", Agent: "coder", Event: apmconfig.EventPostToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"false"}`), ToolResponse: exitStatus1},
		// bash call 2: exit status 0 → success
		{Ts: baseTime.Add(2 * time.Second), Session: "s1", Agent: "coder", Event: apmconfig.EventPreToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"true"}`)},
		{Ts: baseTime.Add(3 * time.Second), Session: "s1", Agent: "coder", Event: apmconfig.EventPostToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"true"}`), ToolResponse: exitStatus0},
	}
	d := mustAggregate(t, records, now)

	var bash ToolDetail
	for _, td := range d.Tools {
		if td.Name == "bash" {
			bash = td
		}
	}

	if bash.CallCount != 2 {
		t.Errorf("CallCount: want 2, got %d", bash.CallCount)
	}
	if bash.ErrorCount != 1 {
		t.Errorf("ErrorCount: want 1, got %d", bash.ErrorCount)
	}
	if len(bash.Errors) != 1 {
		t.Errorf("len(Errors): want 1, got %d", len(bash.Errors))
	}
	if len(bash.RecentCalls) != 1 {
		t.Errorf("len(RecentCalls): want 1, got %d", len(bash.RecentCalls))
	}
	// Duration must be preserved on the error call
	if bash.Errors[0].Duration != JSONDuration(1*time.Second) {
		t.Errorf("Errors[0].Duration: want 1s, got %v", bash.Errors[0].Duration)
	}
	// Timeline: preToolUse for the error call must be flagged
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(d.Sessions))
	}
	tl := d.Sessions[0].Timeline
	// Find the preToolUse for "false" (index 0 in timeline)
	if !tl[0].IsError {
		t.Errorf("timeline[0] (pre for 'false'): want IsError=true, got false")
	}
	if tl[2].IsError {
		t.Errorf("timeline[2] (pre for 'true'): want IsError=false, got true")
	}
	// Agent ToolErrorCnt must include exit_status errors
	if len(d.Agents) != 1 {
		t.Fatalf("want 1 agent, got %d", len(d.Agents))
	}
	if d.Agents[0].ToolErrorCnt != 1 {
		t.Errorf("ToolErrorCnt: want 1, got %d", d.Agents[0].ToolErrorCnt)
	}
}

func TestAggregateDetail_ErrorInvariant(t *testing.T) {
	t.Parallel()
	// Mix: 1 success match, 1 exit_status error match, 1 unmatched pre (crash)
	now := baseTime.Add(30 * time.Minute)
	exitStatus2 := json.RawMessage(`{"items":[{"Json":{"exit_status":"exit status: 2"}}]}`)
	records := []Record{
		// call 1: matched success
		{Ts: baseTime, Session: "s1", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"ok"}`)},
		{Ts: baseTime.Add(1 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"ok"}`)},
		// call 2: matched exit_status error
		{Ts: baseTime.Add(2 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"fail"}`)},
		{Ts: baseTime.Add(3 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"fail"}`), ToolResponse: exitStatus2},
		// call 3: unmatched pre (crash detection)
		{Ts: baseTime.Add(4 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"crash"}`)},
	}
	d := mustAggregate(t, records, now)

	var bash ToolDetail
	for _, td := range d.Tools {
		if td.Name == "bash" {
			bash = td
		}
	}

	// Invariant: ErrorCount == len(Errors)
	if bash.ErrorCount != len(bash.Errors) {
		t.Errorf("invariant violated: ErrorCount=%d != len(Errors)=%d", bash.ErrorCount, len(bash.Errors))
	}
	// 2 errors: 1 exit_status + 1 unmatched
	if bash.ErrorCount != 2 {
		t.Errorf("ErrorCount: want 2, got %d", bash.ErrorCount)
	}
	// 1 success in RecentCalls
	if len(bash.RecentCalls) != 1 {
		t.Errorf("len(RecentCalls): want 1, got %d", len(bash.RecentCalls))
	}
	// No call appears in both Errors and RecentCalls
	errorTimes := map[time.Time]bool{}
	for _, c := range bash.Errors {
		errorTimes[c.Ts] = true
	}
	for _, c := range bash.RecentCalls {
		if errorTimes[c.Ts] {
			t.Errorf("call at %v appears in both Errors and RecentCalls", c.Ts)
		}
	}
	// CallCount unchanged
	if bash.CallCount != 3 {
		t.Errorf("CallCount: want 3, got %d", bash.CallCount)
	}

	// AggregateToolsFromTimeline must satisfy the same invariant
	details, _ := AggregateToolsFromTimeline(d.Sessions)
	for _, td := range details {
		if td.ErrorCount != len(td.Errors) {
			t.Errorf("AggregateToolsFromTimeline invariant violated for %s: ErrorCount=%d != len(Errors)=%d",
				td.Name, td.ErrorCount, len(td.Errors))
		}
	}
}

func TestFinalizeToolDetails(t *testing.T) {
	t.Parallel()
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		td := &ToolDetail{}
		finalizeToolDetails(td)
		if td.ErrorRate != 0 || td.AvgDuration != 0 || td.RecentCalls != nil || td.Errors != nil {
			t.Errorf("empty td mutated unexpectedly: %+v", td)
		}
	})

	t.Run("normal", func(t *testing.T) {
		t.Parallel()
		td := &ToolDetail{
			ToolMetric: ToolMetric{CallCount: 10, ErrorCount: 3},
		}
		// 20 recent calls with ascending timestamps
		for i := range 20 {
			td.RecentCalls = append(td.RecentCalls, ToolCall{
				Ts:       baseTime.Add(time.Duration(i) * time.Second),
				Duration: JSONDuration(time.Duration(i+1) * time.Second),
			})
		}
		// 10 error calls with ascending timestamps
		for i := range 10 {
			td.Errors = append(td.Errors, ToolCall{
				Ts: baseTime.Add(time.Duration(i) * time.Second),
			})
		}
		finalizeToolDetails(td)

		if td.ErrorRate != 0.3 {
			t.Errorf("ErrorRate: want 0.3, got %v", td.ErrorRate)
		}
		// avg of 1..20 seconds = 10.5s
		wantAvg := JSONDuration((1 + 2 + 3 + 4 + 5 + 6 + 7 + 8 + 9 + 10 + 11 + 12 + 13 + 14 + 15 + 16 + 17 + 18 + 19 + 20) * int(time.Second) / 20)
		if td.AvgDuration != wantAvg {
			t.Errorf("AvgDuration: want %v, got %v", wantAvg, td.AvgDuration)
		}
		// 20 < maxRecentCalls=100, so no truncation
		if len(td.RecentCalls) != 20 {
			t.Errorf("RecentCalls len: want 20, got %d", len(td.RecentCalls))
		}
		// newest-first: first element should have the largest timestamp
		if !td.RecentCalls[0].Ts.After(td.RecentCalls[1].Ts) {
			t.Errorf("RecentCalls not newest-first")
		}
		if len(td.Errors) != 10 {
			t.Errorf("Errors len: want 10, got %d", len(td.Errors))
		}
		// newest-first
		if !td.Errors[0].Ts.After(td.Errors[1].Ts) {
			t.Errorf("Errors not newest-first")
		}
	})

	t.Run("zero-count", func(t *testing.T) {
		t.Parallel()
		td := &ToolDetail{ToolMetric: ToolMetric{CallCount: 0, ErrorCount: 0}}
		finalizeToolDetails(td)
		if td.ErrorRate != 0 {
			t.Errorf("ErrorRate: want 0 for zero CallCount, got %v", td.ErrorRate)
		}
	})
}

func TestParseToolResponseError_JsonWireFormat(t *testing.T) {
	t.Parallel()
	// Guard the on-wire "Json" key: renaming the Go field must not break
	// deserialization of the existing wire format.
	raw := json.RawMessage(`{"items":[{"Json":{"exit_status":"exit status: 1"}}]}`)
	if !parseToolResponseError(raw) {
		t.Error("parseToolResponseError returned false for wire key \"Json\" with non-zero exit status")
	}

	// Zero exit status must return false.
	rawZero := json.RawMessage(`{"items":[{"Json":{"exit_status":"exit status: 0"}}]}`)
	if parseToolResponseError(rawZero) {
		t.Error("parseToolResponseError returned true for exit status 0")
	}

	// Missing Json key must return false.
	rawMissing := json.RawMessage(`{"items":[{}]}`)
	if parseToolResponseError(rawMissing) {
		t.Error("parseToolResponseError returned true for missing Json key")
	}
}

func TestAggregateToolTimeseries(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	min := time.Minute

	tests := []struct {
		name    string
		calls   []ToolCall
		wantNil bool
		wantLen int
		check   func(t *testing.T, pts []TimeseriesPoint)
	}{
		{
			name:    "nil input",
			calls:   nil,
			wantNil: true,
		},
		{
			name: "insufficient/single bucket",
			calls: []ToolCall{
				{Ts: t0, Duration: JSONDuration(time.Second)},
				{Ts: t0.Add(30 * time.Second), Duration: JSONDuration(2 * time.Second)},
			},
			wantNil: true,
		},
		{
			name: "two buckets 1min",
			calls: []ToolCall{
				{Ts: t0, Duration: JSONDuration(2 * time.Second)},
				{Ts: t0.Add(min), Duration: JSONDuration(4 * time.Second)},
				{Ts: t0.Add(min + 10*time.Second), IsError: true},
			},
			wantLen: 2,
			check: func(t *testing.T, pts []TimeseriesPoint) {
				if pts[0].Count != 1 || pts[0].ErrorCount != 0 {
					t.Errorf("bucket0: want count=1 err=0, got %+v", pts[0])
				}
				if pts[1].Count != 2 || pts[1].ErrorCount != 1 {
					t.Errorf("bucket1: want count=2 err=1, got %+v", pts[1])
				}
				if pts[1].AvgDuration != JSONDuration(4*time.Second) {
					t.Errorf("bucket1 avgDuration: want 4s, got %v", pts[1].AvgDuration)
				}
			},
		},
		{
			name: "5min bucket for window > 2h",
			// calls at t0, t0+90min, t0+150min → window=150min > 2h → 5min buckets
			calls: []ToolCall{
				{Ts: t0, Duration: JSONDuration(2 * time.Second)},
				{Ts: t0.Add(90 * min), Duration: JSONDuration(4 * time.Second)},
				{Ts: t0.Add(150 * min), Duration: JSONDuration(6 * time.Second)},
			},
			wantLen: 3,
			check: func(t *testing.T, pts []TimeseriesPoint) {
				// each call lands in a distinct 5min bucket
				if pts[0].Bucket != t0.Truncate(5*min) {
					t.Errorf("bucket0: want %v, got %v", t0.Truncate(5*min), pts[0].Bucket)
				}
				if pts[1].Bucket != t0.Add(90*min).Truncate(5*min) {
					t.Errorf("bucket1: want %v, got %v", t0.Add(90*min).Truncate(5*min), pts[1].Bucket)
				}
				if pts[2].Bucket != t0.Add(150*min).Truncate(5*min) {
					t.Errorf("bucket2: want %v, got %v", t0.Add(150*min).Truncate(5*min), pts[2].Bucket)
				}
				// each bucket has 1 call with correct AvgDuration
				if pts[0].Count != 1 || pts[0].AvgDuration != JSONDuration(2*time.Second) {
					t.Errorf("bucket0: want count=1 avg=2s, got %+v", pts[0])
				}
				if pts[1].Count != 1 || pts[1].AvgDuration != JSONDuration(4*time.Second) {
					t.Errorf("bucket1: want count=1 avg=4s, got %+v", pts[1])
				}
				if pts[2].Count != 1 || pts[2].AvgDuration != JSONDuration(6*time.Second) {
					t.Errorf("bucket2: want count=1 avg=6s, got %+v", pts[2])
				}
			},
		},
		{
			name: "error-only bucket has avgDuration 0",
			calls: []ToolCall{
				{Ts: t0, IsError: true},
				{Ts: t0.Add(min), IsError: true},
			},
			wantLen: 2,
			check: func(t *testing.T, pts []TimeseriesPoint) {
				for _, p := range pts {
					if p.AvgDuration != 0 {
						t.Errorf("expected avgDuration=0 for error-only bucket, got %v", p.AvgDuration)
					}
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pts := AggregateToolTimeseries(tc.calls, t0.Add(time.Hour))
			if tc.wantNil {
				if pts != nil {
					t.Errorf("want nil, got %v", pts)
				}
				return
			}
			if len(pts) != tc.wantLen {
				t.Errorf("want %d points, got %d: %v", tc.wantLen, len(pts), pts)
			}
			if tc.check != nil {
				tc.check(t, pts)
			}
		})
	}
}

func TestAggregateToolInputPatterns(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		calls []ToolCall
		topN  int
		want  []PatternCount
	}{
		{
			name:  "nil input",
			calls: nil,
			topN:  5,
			want:  []PatternCount{},
		},
		{
			name: "empty summary becomes (empty)",
			calls: []ToolCall{
				{Ts: t0, InputSummary: ""},
				{Ts: t0.Add(time.Second), InputSummary: ""},
			},
			topN: 5,
			want: []PatternCount{{Summary: "(empty)", Count: 2, LastTs: t0.Add(time.Second)}},
		},
		{
			name: "sort: count desc then lastTs desc then summary asc",
			calls: []ToolCall{
				{Ts: t0, InputSummary: "b"},
				{Ts: t0, InputSummary: "b"},
				{Ts: t0, InputSummary: "a"},
				{Ts: t0, InputSummary: "a"},
				{Ts: t0.Add(time.Second), InputSummary: "c"},
				{Ts: t0.Add(time.Second), InputSummary: "c"},
			},
			topN: 5,
			want: []PatternCount{
				{Summary: "c", Count: 2, LastTs: t0.Add(time.Second)},
				{Summary: "a", Count: 2, LastTs: t0},
				{Summary: "b", Count: 2, LastTs: t0},
			},
		},
		{
			name: "topN limits results",
			calls: []ToolCall{
				{Ts: t0, InputSummary: "x"},
				{Ts: t0, InputSummary: "x"},
				{Ts: t0, InputSummary: "y"},
			},
			topN: 1,
			want: []PatternCount{{Summary: "x", Count: 2, LastTs: t0}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := AggregateToolInputPatterns(tc.calls, tc.topN)
			if len(got) != len(tc.want) {
				t.Fatalf("want %d patterns, got %d: %v", len(tc.want), len(got), got)
			}
			for i, w := range tc.want {
				g := got[i]
				if g.Summary != w.Summary || g.Count != w.Count || !g.LastTs.Equal(w.LastTs) {
					t.Errorf("[%d] want %+v, got %+v", i, w, g)
				}
			}
		})
	}
}

// --- Task 12: foldSessionIntoAgents per-agent tool aggregation --------------

func TestAggregateAgentToolStats(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	// 2 agents × 3 tools: both agentA and agentB use bash+grep+read.
	// agentA has one unmatched bash (error) to exercise ToolErrorCnt > 0.
	records := []Record{
		// agentA: bash x2 (1 error), grep x1, read x1
		rec("s1", "agentA", apmconfig.EventPreToolUse, "bash", 0),
		rec("s1", "agentA", apmconfig.EventPostToolUse, "bash", 1*time.Minute),
		rec("s1", "agentA", apmconfig.EventPreToolUse, "bash", 2*time.Minute), // unmatched → error
		rec("s1", "agentA", apmconfig.EventPreToolUse, "grep", 3*time.Minute),
		rec("s1", "agentA", apmconfig.EventPostToolUse, "grep", 4*time.Minute),
		rec("s1", "agentA", apmconfig.EventPreToolUse, "read", 5*time.Minute),
		rec("s1", "agentA", apmconfig.EventPostToolUse, "read", 6*time.Minute),
		// agentB: bash x1, grep x1, read x1
		rec("s2", "agentB", apmconfig.EventPreToolUse, "bash", 0),
		rec("s2", "agentB", apmconfig.EventPostToolUse, "bash", 1*time.Minute),
		rec("s2", "agentB", apmconfig.EventPreToolUse, "grep", 2*time.Minute),
		rec("s2", "agentB", apmconfig.EventPostToolUse, "grep", 3*time.Minute),
		rec("s2", "agentB", apmconfig.EventPreToolUse, "read", 4*time.Minute),
		rec("s2", "agentB", apmconfig.EventPostToolUse, "read", 5*time.Minute),
	}
	d := mustAggregate(t, records, now)

	byAgent := map[string]AgentDetail{}
	for _, a := range d.Agents {
		byAgent[a.Name] = a
	}
	if len(byAgent) != 2 {
		t.Fatalf("want 2 agents, got %d", len(byAgent))
	}

	toolMap := func(ts []SessionToolSummary) map[string]SessionToolSummary {
		m := make(map[string]SessionToolSummary, len(ts))
		for _, s := range ts {
			m[s.Tool] = s
		}
		return m
	}

	// agentA: bash(2 calls, 1 error), grep(1), read(1); ToolErrorCnt=1.
	aA := byAgent["agentA"]
	if aA.ToolErrorCnt != 1 {
		t.Errorf("agentA ToolErrorCnt: want 1, got %d", aA.ToolErrorCnt)
	}
	tmA := toolMap(aA.ToolSummary)
	if len(tmA) != 3 {
		t.Errorf("agentA: want 3 tools, got %d: %v", len(tmA), aA.ToolSummary)
	}
	if tmA["bash"].CallCount != 2 || tmA["bash"].ErrorCount != 1 {
		t.Errorf("agentA bash: want 2/1, got %d/%d", tmA["bash"].CallCount, tmA["bash"].ErrorCount)
	}
	if tmA["grep"].CallCount != 1 || tmA["grep"].ErrorCount != 0 {
		t.Errorf("agentA grep: want 1/0, got %d/%d", tmA["grep"].CallCount, tmA["grep"].ErrorCount)
	}
	if tmA["read"].CallCount != 1 || tmA["read"].ErrorCount != 0 {
		t.Errorf("agentA read: want 1/0, got %d/%d", tmA["read"].CallCount, tmA["read"].ErrorCount)
	}

	// agentB: bash(1), grep(1), read(1); ToolErrorCnt=0.
	aB := byAgent["agentB"]
	if aB.ToolErrorCnt != 0 {
		t.Errorf("agentB ToolErrorCnt: want 0, got %d", aB.ToolErrorCnt)
	}
	tmB := toolMap(aB.ToolSummary)
	if len(tmB) != 3 {
		t.Errorf("agentB: want 3 tools, got %d: %v", len(tmB), aB.ToolSummary)
	}
	for _, tool := range []string{"bash", "grep", "read"} {
		if tmB[tool].CallCount != 1 || tmB[tool].ErrorCount != 0 {
			t.Errorf("agentB %s: want 1/0, got %d/%d", tool, tmB[tool].CallCount, tmB[tool].ErrorCount)
		}
	}
}

// --- Task 2: SessionDetail new fields ----------------------------------------

func TestSessionToolSummary(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []Record{
		// bash: 3 calls, 1 error (unmatched pre)
		{Ts: baseTime, Session: "s1", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"ok1"}`)},
		{Ts: baseTime.Add(2 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"ok1"}`)},
		{Ts: baseTime.Add(4 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"ok2"}`)},
		{Ts: baseTime.Add(6 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"ok2"}`)},
		{Ts: baseTime.Add(8 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"fail"}`)}, // unmatched → error
		// grep: 1 call, 0 errors
		{Ts: baseTime.Add(10 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "grep",
			ToolInput: []byte(`{"pattern":"foo"}`)},
		{Ts: baseTime.Add(12 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "grep",
			ToolInput: []byte(`{"pattern":"foo"}`)},
		// read: 2 calls, 0 errors
		{Ts: baseTime.Add(14 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "read",
			ToolInput: []byte(`{"path":"/a"}`)},
		{Ts: baseTime.Add(16 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "read",
			ToolInput: []byte(`{"path":"/a"}`)},
		{Ts: baseTime.Add(18 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "read",
			ToolInput: []byte(`{"path":"/b"}`)},
		{Ts: baseTime.Add(20 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "read",
			ToolInput: []byte(`{"path":"/b"}`)},
	}
	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(d.Sessions))
	}
	ts := d.Sessions[0].ToolSummary
	// 3 tools: bash(3), read(2), grep(1) — sorted by CallCount desc
	if len(ts) != 3 {
		t.Fatalf("want 3 ToolSummary entries, got %d: %v", len(ts), ts)
	}
	if ts[0].Tool != "bash" || ts[0].CallCount != 3 {
		t.Errorf("ts[0]: want bash/3, got %s/%d", ts[0].Tool, ts[0].CallCount)
	}
	if ts[1].Tool != "read" || ts[1].CallCount != 2 {
		t.Errorf("ts[1]: want read/2, got %s/%d", ts[1].Tool, ts[1].CallCount)
	}
	if ts[2].Tool != "grep" || ts[2].CallCount != 1 {
		t.Errorf("ts[2]: want grep/1, got %s/%d", ts[2].Tool, ts[2].CallCount)
	}
	// bash: 1 error, SuccessRate = (3-1)/3
	if ts[0].ErrorCount != 1 {
		t.Errorf("bash ErrorCount: want 1, got %d", ts[0].ErrorCount)
	}
	wantRate := float64(2) / float64(3)
	if ts[0].SuccessRate != wantRate {
		t.Errorf("bash SuccessRate: want %f, got %f", wantRate, ts[0].SuccessRate)
	}
	// grep: 0 errors, SuccessRate = 1.0
	if ts[2].SuccessRate != 1.0 {
		t.Errorf("grep SuccessRate: want 1.0, got %f", ts[2].SuccessRate)
	}
	// bash AvgDuration: 2 matched calls each 2s → avg 2s
	if ts[0].AvgDuration != JSONDuration(2*time.Second) {
		t.Errorf("bash AvgDuration: want 2s, got %v", ts[0].AvgDuration)
	}
}

func TestSessionToolSummaryEmpty(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(10 * time.Minute)
	records := []Record{
		rec("s1", "a", apmconfig.EventAgentSpawn, "", 0),
		rec("s1", "a", apmconfig.EventUserPromptSubmit, "", 1*time.Minute),
	}
	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(d.Sessions))
	}
	if d.Sessions[0].ToolSummary != nil {
		t.Errorf("want nil ToolSummary for session with no tool calls, got %v", d.Sessions[0].ToolSummary)
	}
}

func TestSessionAssistantResponse(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(10 * time.Minute)
	records := []Record{
		rec("s1", "a", apmconfig.EventAgentSpawn, "", 0),
		{
			Ts: baseTime.Add(5 * time.Minute), Session: "s1", Agent: "a",
			Event:             apmconfig.EventStop,
			AssistantResponse: json.RawMessage(`"The task is complete."`),
		},
	}
	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(d.Sessions))
	}
	if got, want := d.Sessions[0].AssistantResponse, "The task is complete."; got != want {
		t.Errorf("AssistantResponse: want %q, got %q", want, got)
	}
}

// --- Task 4: FileChange pipeline integration --------------------------------

func writeRec(session, agent, path, command string, offset time.Duration) Record {
	input, _ := json.Marshal(map[string]string{"command": command, "path": path, "content": "x"})
	return Record{
		Ts:        baseTime.Add(offset),
		Session:   session,
		Agent:     agent,
		Event:     apmconfig.EventPreToolUse,
		Tool:      "write",
		ToolInput: input,
		Cwd:       "/tmp",
	}
}

func TestFilesChangedSessionMetric(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []Record{
		writeRec("s1", "a", "/tmp/foo.go", "create", 0),
		writeRec("s1", "a", "/tmp/bar.go", "strReplace", 1*time.Minute),
		writeRec("s1", "a", "/tmp/foo.go", "strReplace", 2*time.Minute), // duplicate path
	}
	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(d.Sessions))
	}
	if got := d.Sessions[0].FilesChanged; got != 2 {
		t.Errorf("FilesChanged: want 2, got %d", got)
	}
	if got := d.Overview.Sessions[0].FilesChanged; got != 2 {
		t.Errorf("Overview.Sessions[0].FilesChanged: want 2, got %d", got)
	}
}

func TestFilesChangedNoWrite(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []Record{
		rec("s1", "a", apmconfig.EventAgentSpawn, "", 0),
		rec("s1", "a", apmconfig.EventUserPromptSubmit, "", 1*time.Minute),
	}
	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(d.Sessions))
	}
	if got := d.Sessions[0].FilesChanged; got != 0 {
		t.Errorf("FilesChanged: want 0, got %d", got)
	}
	if d.Sessions[0].Changes != nil {
		t.Errorf("Changes: want nil, got %v", d.Sessions[0].Changes)
	}
}

func TestFilesChangedAgentTwoSessions(t *testing.T) {
	t.Parallel()
	// Same agent, 2 sessions, each writes the same file → AgentMetric.FilesChanged == 2
	now := baseTime.Add(1 * time.Hour)
	records := []Record{
		writeRec("s1", "agent1", "/tmp/foo.go", "create", 0),
		writeRec("s2", "agent1", "/tmp/foo.go", "strReplace", 10*time.Minute),
	}
	d := mustAggregate(t, records, now)
	byName := map[string]AgentMetric{}
	for _, a := range d.Overview.Agents {
		byName[a.Name] = a
	}
	if got := byName["agent1"].FilesChanged; got != 2 {
		t.Errorf("AgentMetric.FilesChanged: want 2 (sum of per-session unique counts), got %d", got)
	}
}

func TestSessionDetailChangesChronological(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []Record{
		writeRec("s1", "a", "/tmp/b.go", "create", 2*time.Minute),
		writeRec("s1", "a", "/tmp/a.go", "create", 1*time.Minute),
	}
	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(d.Sessions))
	}
	changes := d.Sessions[0].Changes
	if len(changes) != 2 {
		t.Fatalf("want 2 changes, got %d", len(changes))
	}
	if !changes[0].Ts.Before(changes[1].Ts) {
		t.Errorf("Changes not in Ts ascending order: %v, %v", changes[0].Ts, changes[1].Ts)
	}
	if changes[0].Path != "/tmp/a.go" {
		t.Errorf("Changes[0].Path: want /tmp/a.go, got %s", changes[0].Path)
	}
}

func TestAggregateDetail_CancelledCtx(t *testing.T) {
	t.Parallel()
	records := []Record{
		rec("s1", "agent1", apmconfig.EventAgentSpawn, "", 0),
		rec("s1", "agent1", apmconfig.EventStop, "", 1*time.Minute),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dm, err := AggregateDetail(ctx, records, baseTime.Add(time.Hour))
	if err == nil {
		t.Fatal("expected ctx.Err(), got nil")
	}
	if err != context.Canceled {
		t.Errorf("got %v, want context.Canceled", err)
	}
	if len(dm.Sessions) != 0 {
		t.Errorf("expected zero DetailedMetrics on cancel, got %d sessions", len(dm.Sessions))
	}
}
