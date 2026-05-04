package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

var baseTime = time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)

func rec(session, agent, event, tool string, offset time.Duration) MergedRecord {
	ts := baseTime.Add(offset)
	r := MergedRecord{
		SessionID: session,
		Agent:     agent,
		ToolName:  tool,
	}
	switch event {
	case apmconfig.EventAgentSpawn:
		r.Kind = "prompt"
		r.PromptTs = ts
		r.PromptText = ""
	case apmconfig.EventUserPromptSubmit:
		r.Kind = "prompt"
		r.PromptTs = ts
	case apmconfig.EventPreToolUse:
		r.Kind = "toolUse"
		r.PreToolTs = ts
		r.ToolUseID = fmt.Sprintf("tuid-%s-%s-%s", session, tool, ts)
	case apmconfig.EventPostToolUse:
		r.Kind = "toolResult"
		r.PostToolTs = ts
		r.ToolStatus = "success"
		// ToolUseID must be set by caller for pairing; rec() alone can't pair.
	case apmconfig.EventStop:
		r.Kind = "assistantText"
		r.AssistantText = ""
		r.CreatedAt = ts
	}
	return r
}

// recPair returns a matched toolUse + toolResult pair with the same ToolUseID.
func recPair(session, agent, tool string, preOffset, postOffset time.Duration) (MergedRecord, MergedRecord) {
	tuid := fmt.Sprintf("tuid-%s-%s-%s", session, tool, baseTime.Add(preOffset))
	pre := MergedRecord{
		SessionID: session, Agent: agent, Kind: RecordKindToolUse,
		ToolUseID: tuid, ToolName: tool, PreToolTs: baseTime.Add(preOffset),
	}
	post := MergedRecord{
		SessionID: session, Agent: agent, Kind: RecordKindToolResult,
		ToolUseID: tuid, ToolName: tool, PostToolTs: baseTime.Add(postOffset),
		ToolStatus: ToolStatusSuccess,
	}
	return pre, post
}

func aggregateOverview(records []MergedRecord, now time.Time) Metrics {
	dm, _ := AggregateDetail(context.Background(), records, now)
	return dm.Overview
}

// mustAggregate fails the test if AggregateDetail returns an error.
func mustAggregate(t *testing.T, records []MergedRecord, now time.Time) DetailedMetrics {
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
	m2 := aggregateOverview([]MergedRecord{}, baseTime)
	if m2.Sessions != nil || m2.Tools != nil || m2.Agents != nil || m2.HourlyActivity != nil {
		t.Errorf("expected zero-value Metrics for empty slice, got %+v", m2)
	}
}

func TestAggregateSession(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(10 * time.Minute)
	bashPre, bashPost := recPair("s1", "agent1", "bash", 2*time.Minute, 3*time.Minute)
	records := []MergedRecord{
		rec("s1", "agent1", apmconfig.EventUserPromptSubmit, "", 1*time.Minute),
		bashPre,
		bashPost,
		rec("s1", "agent1", apmconfig.EventStop, "", 4*time.Minute),
		// s2: active (no stop, last event 2min before now)
		rec("s2", "agent2", apmconfig.EventUserPromptSubmit, "", 8*time.Minute),
	}
	// Set prompt text
	records[0].PromptText = "hello"
	records[4].PromptText = "world"

	m := aggregateOverview(records, now)

	byID := map[string]SessionMetric{}
	for _, s := range m.Sessions {
		byID[s.ID] = s
	}

	s1 := byID["s1"]
	if s1.Active {
		t.Error("s1 should not be active (last activity exceeded timeout)")
	}
	if s1.ToolCalls != 1 {
		t.Errorf("s1 ToolCalls: want 1, got %d", s1.ToolCalls)
	}
	if s1.Prompts != 1 {
		t.Errorf("s1 Prompts: want 1, got %d", s1.Prompts)
	}
	wantDur := 3 * time.Minute
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
	records := []MergedRecord{
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
	bash1pre, bash1post := recPair("s1", "a", "bash", 0, 1*time.Minute)
	grepPre, grepPost := recPair("s1", "a", "grep", 3*time.Minute, 4*time.Minute)
	records := []MergedRecord{
		// bash: 2 calls, 1 error (second toolUse has no toolResult)
		bash1pre,
		bash1post,
		rec("s1", "a", apmconfig.EventPreToolUse, "bash", 2*time.Minute),
		// no toolResult for second bash call
		// grep: 1 call, 0 errors
		grepPre,
		grepPost,
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
	bash1pre, bash1post := recPair("s1", "agent1", "bash", 2*time.Minute, 3*time.Minute)
	grepPre, grepPost := recPair("s3", "agent2", "grep", 1*time.Minute, 2*time.Minute)
	records := []MergedRecord{
		// agent1: 2 sessions
		rec("s1", "agent1", apmconfig.EventUserPromptSubmit, "", 1*time.Minute),
		bash1pre,
		bash1post,
		rec("s2", "agent1", apmconfig.EventUserPromptSubmit, "", 6*time.Minute),
		rec("s2", "agent1", apmconfig.EventUserPromptSubmit, "", 7*time.Minute),
		// agent2: 1 session
		grepPre,
		grepPost,
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
	bashPre, bashPost := recPair("s1", "a", "bash", 20*time.Minute, 70*time.Minute)
	records := []MergedRecord{
		rec("s1", "a", apmconfig.EventUserPromptSubmit, "", 10*time.Minute), // 10:10
		bashPre,  // 10:20
		bashPost, // 11:10
		rec("s1", "a", apmconfig.EventStop, "", 80*time.Minute), // 11:20
	}

	m := aggregateOverview(records, now)

	byHour := map[time.Time]int{}
	for _, h := range m.HourlyActivity {
		byHour[h.Hour] = h.EventCount
	}

	hour10 := baseTime.Truncate(time.Hour) // 10:00
	hour11 := hour10.Add(time.Hour)        // 11:00

	if byHour[hour10] != 2 {
		t.Errorf("hour 10: want 2 events, got %d", byHour[hour10])
	}
	if byHour[hour11] != 2 {
		t.Errorf("hour 11: want 2 events, got %d", byHour[hour11])
	}
}

func TestAggregateDetail(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(30 * time.Minute)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "coder", Kind: RecordKindPrompt, PromptTs: baseTime.Add(1 * time.Minute), PromptText: "first", Cwd: "/tmp"},
		{SessionID: "s1", Agent: "coder", Kind: RecordKindPrompt, PromptTs: baseTime.Add(2 * time.Minute), PromptText: "second"},
		{SessionID: "s1", Agent: "coder", Kind: RecordKindToolUse, ToolUseID: "tu-bash", ToolName: "bash", PreToolTs: baseTime.Add(3 * time.Minute)},
		{SessionID: "s1", Agent: "coder", Kind: RecordKindToolResult, ToolUseID: "tu-bash", ToolName: "bash", PostToolTs: baseTime.Add(3*time.Minute + 2*time.Second), ToolStatus: ToolStatusSuccess},
		{SessionID: "s1", Agent: "coder", Kind: RecordKindToolUse, ToolUseID: "tu-grep", ToolName: "grep", PreToolTs: baseTime.Add(4 * time.Minute)}, // unmatched => error
	}

	d := mustAggregate(t, records, now)

	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session detail, got %d", len(d.Sessions))
	}
	sd := d.Sessions[0]
	if sd.Cwd != "/tmp" {
		t.Errorf("cwd: want /tmp, got %q", sd.Cwd)
	}
	if len(sd.PromptHistory) != 2 || sd.PromptHistory[0] != "first" {
		t.Errorf("prompts oldest-first expected, got %v", sd.PromptHistory)
	}
	// Timeline: 4 events (2 prompts + 1 toolUse + 1 unmatched toolUse), last one is the unmatched preToolUse -> IsError true
	if len(sd.Timeline) < 3 {
		t.Fatalf("timeline: want at least 3 events, got %d", len(sd.Timeline))
	}
	lastTool := sd.Timeline[len(sd.Timeline)-1]
	if !lastTool.IsError {
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
	records := []MergedRecord{
		{SessionID: "s1", Agent: "coder", Kind: RecordKindToolUse, ToolUseID: "tu-1", ToolName: "bash", PreToolTs: baseTime, ToolInput: json.RawMessage(`{"command":"echo hi"}`)},
		{SessionID: "s1", Agent: "coder", Kind: RecordKindToolResult, ToolUseID: "tu-1", ToolName: "bash", PostToolTs: baseTime, ToolStatus: ToolStatusSuccess},
	}

	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session detail, got %d", len(d.Sessions))
	}
	sd := d.Sessions[0]
	if len(sd.Timeline) != 1 {
		t.Fatalf("timeline: want 1 event (toolUse), got %d", len(sd.Timeline))
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
	records := []MergedRecord{
		// s1: starts earliest, last activity at +10min
		rec("s1", "a", apmconfig.EventUserPromptSubmit, "", 0),
		rec("s1", "a", apmconfig.EventStop, "", 10*time.Minute),
		// s2: starts later, last activity at +60min
		rec("s2", "a", apmconfig.EventUserPromptSubmit, "", 30*time.Minute),
		rec("s2", "a", apmconfig.EventStop, "", 60*time.Minute),
		// s3: starts in middle, last activity at +90min (newest)
		rec("s3", "a", apmconfig.EventUserPromptSubmit, "", 20*time.Minute),
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
		{"truncates", `{"__tool_use_purpose":"` + strings.Repeat("x", 200) + `"}`, strings.Repeat("x", 117) + "…"},
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
	records := []MergedRecord{
		{SessionID: "s", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-1", ToolName: "read", PreToolTs: baseTime,
			ToolInput: []byte(`{"__tool_use_purpose":"read plan"}`)},
		{SessionID: "s", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-1", ToolName: "read", PostToolTs: baseTime.Add(time.Second), ToolStatus: ToolStatusSuccess},
		// Second call: unmatched → error, with a different summary source.
		{SessionID: "s", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-2", ToolName: "read", PreToolTs: baseTime.Add(2 * time.Minute),
			ToolInput: []byte(`{"operations":[{"path":"/x/y/SKILL.md"}]}`)},
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
	records := []MergedRecord{
		{SessionID: "s", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-1", ToolName: "read", PreToolTs: baseTime,
			ToolInput: []byte(`{"operations":[{"path":".kiro/skills/task-verification/SKILL.md"}]}`)},
		{SessionID: "s", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-1", ToolName: "read", PostToolTs: baseTime.Add(1 * time.Second), ToolStatus: ToolStatusSuccess},
		{SessionID: "s", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-2", ToolName: "read", PreToolTs: baseTime.Add(2 * time.Second),
			ToolInput: []byte(`{"operations":[{"path":"/abs/.kiro/skills/task-verification/SKILL.md"}]}`)},
		{SessionID: "s", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-2", ToolName: "read", PostToolTs: baseTime.Add(3 * time.Second), ToolStatus: ToolStatusSuccess},
		{SessionID: "s", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-3", ToolName: "read", PreToolTs: baseTime.Add(4 * time.Second),
			ToolInput: []byte(`{"operations":[{"path":".kiro/skills/git-master/SKILL.md"}]}`)},
		{SessionID: "s", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-3", ToolName: "read", PostToolTs: baseTime.Add(5 * time.Second), ToolStatus: ToolStatusSuccess},
		// unrelated: read of a non-SKILL.md path
		{SessionID: "s", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-4", ToolName: "read", PreToolTs: baseTime.Add(6 * time.Second),
			ToolInput: []byte(`{"operations":[{"path":"/tmp/foo.go"}]}`)},
		{SessionID: "s", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-4", ToolName: "read", PostToolTs: baseTime.Add(7 * time.Second), ToolStatus: ToolStatusSuccess},
		// tool != "read" should not count
		{SessionID: "s", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-5", ToolName: "shell", PreToolTs: baseTime.Add(8 * time.Second),
			ToolInput: []byte(`{"command":"cat code-search/SKILL.md"}`)},
		{SessionID: "s", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-5", ToolName: "shell", PostToolTs: baseTime.Add(9 * time.Second), ToolStatus: ToolStatusSuccess},
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
	records := []MergedRecord{
		{SessionID: "s", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-1", ToolName: "bash", PreToolTs: baseTime,
			ToolInput: []byte(`{"command":"echo hello"}`)},
		{SessionID: "s", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-1", ToolName: "bash", PostToolTs: baseTime.Add(time.Second), ToolStatus: ToolStatusSuccess},
		{SessionID: "s", Agent: "a", Kind: RecordKindPrompt, PromptTs: baseTime.Add(2 * time.Second)},
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
	records := []MergedRecord{
		{SessionID: "s", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-1", ToolName: "read", PreToolTs: baseTime,
			ToolInput: []byte(`{"operations":[{"path":"/tmp/foo.go"}]}`)},
		{SessionID: "s", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-1", ToolName: "read", PostToolTs: baseTime.Add(1 * time.Second), ToolStatus: ToolStatusSuccess},
	}
	d := mustAggregate(t, records, now)
	if len(d.Skills) != 0 {
		t.Errorf("want 0 skills, got %+v", d.Skills)
	}
}

func TestAggregateDetailCap(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(24 * time.Hour)
	var records []MergedRecord
	// 150 matched calls for "bash" (toolUse+toolResult pairs)
	for i := 0; i < 150; i++ {
		offset := time.Duration(i) * time.Second
		pre, post := recPair("s1", "a", "bash", offset, offset+500*time.Millisecond)
		records = append(records, pre, post)
	}
	// 75 unmatched toolUse for "grep" → errors
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
	records := []MergedRecord{
		{SessionID: "s", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-diag", ToolName: "code", PreToolTs: baseTime,
			ToolInput: []byte(`{"operation":"get_diagnostics"}`)},
		{SessionID: "s", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-hover", ToolName: "code", PreToolTs: baseTime.Add(1 * time.Second),
			ToolInput: []byte(`{"operation":"get_hover"}`)},
		{SessionID: "s", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-hover", ToolName: "code", PostToolTs: baseTime.Add(2 * time.Second), ToolStatus: ToolStatusSuccess},
		{SessionID: "s", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-diag", ToolName: "code", PostToolTs: baseTime.Add(10 * time.Second), ToolStatus: ToolStatusSuccess},
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
	records := []MergedRecord{
		{SessionID: "s", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-sleep", ToolName: "shell", PreToolTs: baseTime,
			ToolInput: []byte(`{"command":"sleep 10"}`)},
		{SessionID: "s", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-echo", ToolName: "shell", PreToolTs: baseTime.Add(1 * time.Second),
			ToolInput: []byte(`{"command":"echo fast"}`)},
		{SessionID: "s", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-echo", ToolName: "shell", PostToolTs: baseTime.Add(2 * time.Second), ToolStatus: ToolStatusSuccess},
		{SessionID: "s", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-sleep", ToolName: "shell", PostToolTs: baseTime.Add(10 * time.Second), ToolStatus: ToolStatusSuccess},
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

func BenchmarkAggregate(b *testing.B) {
	// Build a synthetic dataset for benchmarking.
	var recs []MergedRecord
	for i := 0; i < 500; i++ {
		pre, post := recPair("s1", "a", "bash", time.Duration(i)*time.Second, time.Duration(i)*time.Second+500*time.Millisecond)
		recs = append(recs, pre, post)
	}
	if len(recs) == 0 {
		b.Fatal("no records")
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

// TestMixedAgentFixture verifies that two agents under the same session ID
// produce two distinct SessionDetails with correct AgentKey and Title fields.
func TestMixedAgentFixture(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	orcBashPre, orcBashPost := recPair("ma-session-001", "orchestrator", "shell", 1*time.Minute, 2*time.Minute)
	orcReadPre, orcReadPost := recPair("ma-session-001", "orchestrator", "read", 3*time.Minute, 4*time.Minute)
	orcShell2Pre, orcShell2Post := recPair("ma-session-001", "orchestrator", "shell", 5*time.Minute, 6*time.Minute)
	orcGrepPre := rec("ma-session-001", "orchestrator", apmconfig.EventPreToolUse, "grep", 7*time.Minute) // unmatched
	leadReadPre, leadReadPost := recPair("ma-session-001", "lead", "read", 10*time.Minute, 11*time.Minute)
	leadShellPre, leadShellPost := recPair("ma-session-001", "lead", "shell", 12*time.Minute, 13*time.Minute)
	mixed := []MergedRecord{
		{SessionID: "ma-session-001", Agent: "orchestrator", Kind: RecordKindPrompt, PromptTs: baseTime, PromptText: "orchestrate the build pipeline"},
		orcBashPre, orcBashPost, orcReadPre, orcReadPost, orcShell2Pre, orcShell2Post, orcGrepPre,
		{SessionID: "ma-session-001", Agent: "lead", Kind: RecordKindPrompt, PromptTs: baseTime.Add(9 * time.Minute), PromptText: "implement the assigned subtask"},
		leadReadPre, leadReadPost, leadShellPre, leadShellPost,
	}

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
	// orchestrator: 3 matched tool calls + 1 unmatched grep
	if orc.ToolCalls != 4 {
		t.Errorf("orc.ToolCalls: want 4, got %d", orc.ToolCalls)
	}
	// lead: 2 matched tool calls
	if lead.ToolCalls != 2 {
		t.Errorf("lead.ToolCalls: want 2, got %d", lead.ToolCalls)
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
	records := []MergedRecord{
		// s1/agentA: read x2 (both matched), bash x1 (unmatched → error)
	}
	r1pre, r1post := recPair("s1", "agentA", "read", 0, 1*time.Minute)
	r2pre, r2post := recPair("s1", "agentA", "read", 2*time.Minute, 3*time.Minute)
	bashUnmatched := rec("s1", "agentA", apmconfig.EventPreToolUse, "bash", 4*time.Minute)
	r3pre, r3post := recPair("s2", "agentB", "read", 10*time.Minute, 11*time.Minute)
	records = append(records, r1pre, r1post, r2pre, r2post, bashUnmatched, r3pre, r3post)
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
	records := []MergedRecord{
		// Same sid "s1" under two agents — must yield two SessionDetails.
		{SessionID: "s1", Agent: "orchestrator", Kind: RecordKindPrompt, PromptTs: baseTime, PromptText: "orc prompt"},
		{SessionID: "s1", Agent: "orchestrator", Kind: RecordKindToolUse, ToolUseID: "tu-bash", ToolName: "bash", PreToolTs: baseTime.Add(1 * time.Minute)},
		{SessionID: "s1", Agent: "orchestrator", Kind: RecordKindToolResult, ToolUseID: "tu-bash", ToolName: "bash", PostToolTs: baseTime.Add(2 * time.Minute), ToolStatus: ToolStatusSuccess},
		{SessionID: "s1", Agent: "lead", Kind: RecordKindPrompt, PromptTs: baseTime.Add(3 * time.Minute), PromptText: "lead prompt"},
		{SessionID: "s1", Agent: "lead", Kind: RecordKindToolUse, ToolUseID: "tu-grep", ToolName: "grep", PreToolTs: baseTime.Add(4 * time.Minute)},
		{SessionID: "s1", Agent: "lead", Kind: RecordKindToolResult, ToolUseID: "tu-grep", ToolName: "grep", PostToolTs: baseTime.Add(5 * time.Minute), ToolStatus: ToolStatusSuccess},
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

func TestAggregateAbsorbsFirstPromptOnlyAgentSplit(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	readPre, readPost := recPair("s1", "lead", "read", 1*time.Minute, 2*time.Minute)
	writePre, writePost := recPair("s1", "lead", "write", 3*time.Minute, 4*time.Minute)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "auto", Kind: RecordKindPrompt, PromptTs: baseTime, PromptText: "run once"},
		readPre,
		readPost,
		writePre,
		writePost,
	}

	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 SessionDetail, got %d: %#v", len(d.Sessions), d.Sessions)
	}
	s := d.Sessions[0]
	if s.Agent != "lead" || s.AgentKey != "s1|lead" {
		t.Fatalf("session agent = %q key = %q, want lead / s1|lead", s.Agent, s.AgentKey)
	}
	if s.Prompts != 1 || len(s.PromptHistory) != 1 || s.PromptHistory[0] != "run once" {
		t.Fatalf("prompt was not preserved: prompts=%d history=%#v", s.Prompts, s.PromptHistory)
	}
	if s.ToolCalls != 2 {
		t.Fatalf("ToolCalls = %d, want 2", s.ToolCalls)
	}
	if len(s.Timeline) != 3 || s.Timeline[0].Event != apmconfig.EventUserPromptSubmit {
		t.Fatalf("timeline did not keep first prompt before tools: %#v", s.Timeline)
	}
}

func TestAggregateAbsorbsFirstPromptOnlySessionMetaAgentSplit(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	readPre, readPost := recPair("s1", "lead", "read", 1*time.Minute, 2*time.Minute)
	records := []MergedRecord{
		{
			SessionID:   "s1",
			Agent:       "auto",
			Kind:        RecordKindSessionMeta,
			CreatedAt:   baseTime,
			UpdatedAt:   baseTime.Add(2 * time.Minute),
			PromptTexts: []string{"run once"},
		},
		readPre,
		readPost,
	}

	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 SessionDetail, got %d: %#v", len(d.Sessions), d.Sessions)
	}
	s := d.Sessions[0]
	if s.Agent != "lead" || s.AgentKey != "s1|lead" {
		t.Fatalf("session agent = %q key = %q, want lead / s1|lead", s.Agent, s.AgentKey)
	}
	if s.Prompts != 1 || len(s.PromptHistory) != 1 || s.PromptHistory[0] != "run once" {
		t.Fatalf("prompt was not preserved: prompts=%d history=%#v", s.Prompts, s.PromptHistory)
	}
	if s.ToolCalls != 1 {
		t.Fatalf("ToolCalls = %d, want 1", s.ToolCalls)
	}
}

func TestAggregateKeepsRealMultiAgentWhenFirstAgentHasTool(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	autoPre, autoPost := recPair("s1", "auto", "read", 1*time.Minute, 2*time.Minute)
	leadPre, leadPost := recPair("s1", "lead", "write", 3*time.Minute, 4*time.Minute)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "auto", Kind: RecordKindPrompt, PromptTs: baseTime, PromptText: "auto prompt"},
		autoPre,
		autoPost,
		leadPre,
		leadPost,
	}

	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 2 {
		t.Fatalf("want 2 SessionDetails, got %d: %#v", len(d.Sessions), d.Sessions)
	}
	byAgent := map[string]SessionDetail{}
	for _, s := range d.Sessions {
		byAgent[s.Agent] = s
	}
	if byAgent["auto"].ToolCalls != 1 || byAgent["lead"].ToolCalls != 1 {
		t.Fatalf("real multi-agent tool calls were collapsed: %#v", byAgent)
	}
}

func TestAggregateKeepsPromptOnlyAgentsSplit(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "auto", Kind: RecordKindPrompt, PromptTs: baseTime, PromptText: "auto prompt"},
		{SessionID: "s1", Agent: "lead", Kind: RecordKindPrompt, PromptTs: baseTime.Add(time.Minute), PromptText: "lead prompt"},
	}

	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 2 {
		t.Fatalf("want 2 prompt-only SessionDetails, got %d: %#v", len(d.Sessions), d.Sessions)
	}
	byAgent := map[string]SessionDetail{}
	for _, s := range d.Sessions {
		byAgent[s.Agent] = s
	}
	if byAgent["auto"].Prompts != 1 || byAgent["lead"].Prompts != 1 {
		t.Fatalf("prompt-only agents were collapsed: %#v", byAgent)
	}
}

func TestAggregateAbsorbsPromptOnlyAgentIntoAssistantAgent(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "auto", Kind: RecordKindPrompt, PromptTs: baseTime, PromptText: "run once"},
		{SessionID: "s1", Agent: "lead", Kind: RecordKindAssistantText, CreatedAt: baseTime.Add(time.Minute), AssistantText: "done"},
	}

	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 SessionDetail, got %d: %#v", len(d.Sessions), d.Sessions)
	}
	s := d.Sessions[0]
	if s.Agent != "lead" || s.Prompts != 1 || s.AssistantResponse != "done" {
		t.Fatalf("prompt-only agent was not absorbed into substantive agent: %#v", s)
	}
}

func TestAggregateKeepsEqualStartAgentSplit(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "auto", Kind: RecordKindPrompt, PromptTs: baseTime, PromptText: "auto prompt"},
		{SessionID: "s1", Agent: "lead", Kind: RecordKindToolUse, ToolUseID: "tu-read", ToolName: "read", PreToolTs: baseTime},
		{SessionID: "s1", Agent: "lead", Kind: RecordKindToolResult, ToolUseID: "tu-read", ToolName: "read", PostToolTs: baseTime.Add(time.Minute), ToolStatus: ToolStatusSuccess},
	}

	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 2 {
		t.Fatalf("want 2 equal-start SessionDetails, got %d: %#v", len(d.Sessions), d.Sessions)
	}
	byAgent := map[string]SessionDetail{}
	for _, s := range d.Sessions {
		byAgent[s.Agent] = s
	}
	if byAgent["auto"].Prompts != 1 || byAgent["lead"].ToolCalls != 1 {
		t.Fatalf("equal-start agents were collapsed: %#v", byAgent)
	}
}

func TestAggregateSummaryToolTitlePreferred(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "coder", Kind: RecordKindPrompt, PromptTs: baseTime, PromptText: "first prompt"},
		{SessionID: "s1", Agent: "coder", Kind: RecordKindToolUse, ToolUseID: "tu-sum", ToolName: "summary", PreToolTs: baseTime.Add(1 * time.Minute),
			ToolInput: []byte(`{"taskDescription":"implement feature X"}`)},
		{SessionID: "s1", Agent: "coder", Kind: RecordKindToolResult, ToolUseID: "tu-sum", ToolName: "summary", PostToolTs: baseTime.Add(2 * time.Minute), ToolStatus: ToolStatusSuccess},
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
	records := []MergedRecord{
		{SessionID: "s1", Agent: "", Kind: RecordKindPrompt, PromptTs: baseTime, PromptText: "hi"},
		{SessionID: "s1", Agent: "", Kind: RecordKindToolUse, ToolUseID: "tu-bash", ToolName: "bash", PreToolTs: baseTime.Add(1 * time.Minute)},
		{SessionID: "s1", Agent: "", Kind: RecordKindToolResult, ToolUseID: "tu-bash", ToolName: "bash", PostToolTs: baseTime.Add(2 * time.Minute), ToolStatus: ToolStatusSuccess},
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
	records := []MergedRecord{
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
	records := []MergedRecord{
		// bash call 1: error
		{SessionID: "s1", Agent: "coder", Kind: RecordKindToolUse, ToolUseID: "tu-false", ToolName: "bash", PreToolTs: baseTime,
			ToolInput: []byte(`{"command":"false"}`)},
		{SessionID: "s1", Agent: "coder", Kind: RecordKindToolResult, ToolUseID: "tu-false", ToolName: "bash", PostToolTs: baseTime.Add(1 * time.Second),
			ToolStatus: ToolStatusError, ErrorDetail: "exit 1"},
		// bash call 2: success
		{SessionID: "s1", Agent: "coder", Kind: RecordKindToolUse, ToolUseID: "tu-true", ToolName: "bash", PreToolTs: baseTime.Add(2 * time.Second),
			ToolInput: []byte(`{"command":"true"}`)},
		{SessionID: "s1", Agent: "coder", Kind: RecordKindToolResult, ToolUseID: "tu-true", ToolName: "bash", PostToolTs: baseTime.Add(3 * time.Second),
			ToolStatus: ToolStatusSuccess},
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
	// Find the preToolUse entries
	if len(tl) < 2 {
		t.Fatalf("timeline: want at least 2 events, got %d", len(tl))
	}
	if !tl[0].IsError {
		t.Errorf("timeline[0] (pre for 'false'): want IsError=true, got false")
	}
	if tl[1].IsError {
		t.Errorf("timeline[1] (pre for 'true'): want IsError=false, got true")
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
	now := baseTime.Add(30 * time.Minute)
	records := []MergedRecord{
		// call 1: matched success
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-ok", ToolName: "bash", PreToolTs: baseTime,
			ToolInput: []byte(`{"command":"ok"}`)},
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-ok", ToolName: "bash", PostToolTs: baseTime.Add(1 * time.Second),
			ToolStatus: ToolStatusSuccess},
		// call 2: matched error
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-fail", ToolName: "bash", PreToolTs: baseTime.Add(2 * time.Second),
			ToolInput: []byte(`{"command":"fail"}`)},
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-fail", ToolName: "bash", PostToolTs: baseTime.Add(3 * time.Second),
			ToolStatus: ToolStatusError, ErrorDetail: "exit 2"},
		// call 3: unmatched pre (crash detection)
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-crash", ToolName: "bash", PreToolTs: baseTime.Add(4 * time.Second),
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
	bashA1pre, bashA1post := recPair("s1", "agentA", "bash", 0, 1*time.Minute)
	bashA2 := rec("s1", "agentA", apmconfig.EventPreToolUse, "bash", 2*time.Minute) // unmatched → error
	grepApre, grepApost := recPair("s1", "agentA", "grep", 3*time.Minute, 4*time.Minute)
	readApre, readApost := recPair("s1", "agentA", "read", 5*time.Minute, 6*time.Minute)
	bashBpre, bashBpost := recPair("s2", "agentB", "bash", 0, 1*time.Minute)
	grepBpre, grepBpost := recPair("s2", "agentB", "grep", 2*time.Minute, 3*time.Minute)
	readBpre, readBpost := recPair("s2", "agentB", "read", 4*time.Minute, 5*time.Minute)
	records := []MergedRecord{
		bashA1pre, bashA1post,
		bashA2,
		grepApre, grepApost,
		readApre, readApost,
		bashBpre, bashBpost,
		grepBpre, grepBpost,
		readBpre, readBpost,
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
	records := []MergedRecord{
		// bash: 3 calls, 1 error (unmatched pre)
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-ok1", ToolName: "bash", PreToolTs: baseTime,
			ToolInput: []byte(`{"command":"ok1"}`)},
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-ok1", ToolName: "bash", PostToolTs: baseTime.Add(2 * time.Second), ToolStatus: ToolStatusSuccess},
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-ok2", ToolName: "bash", PreToolTs: baseTime.Add(4 * time.Second),
			ToolInput: []byte(`{"command":"ok2"}`)},
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-ok2", ToolName: "bash", PostToolTs: baseTime.Add(6 * time.Second), ToolStatus: ToolStatusSuccess},
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-fail", ToolName: "bash", PreToolTs: baseTime.Add(8 * time.Second),
			ToolInput: []byte(`{"command":"fail"}`)}, // unmatched → error
		// grep: 1 call, 0 errors
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-grep", ToolName: "grep", PreToolTs: baseTime.Add(10 * time.Second),
			ToolInput: []byte(`{"pattern":"foo"}`)},
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-grep", ToolName: "grep", PostToolTs: baseTime.Add(12 * time.Second), ToolStatus: ToolStatusSuccess},
		// read: 2 calls, 0 errors
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-read1", ToolName: "read", PreToolTs: baseTime.Add(14 * time.Second),
			ToolInput: []byte(`{"path":"/a"}`)},
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-read1", ToolName: "read", PostToolTs: baseTime.Add(16 * time.Second), ToolStatus: ToolStatusSuccess},
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-read2", ToolName: "read", PreToolTs: baseTime.Add(18 * time.Second),
			ToolInput: []byte(`{"path":"/b"}`)},
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-read2", ToolName: "read", PostToolTs: baseTime.Add(20 * time.Second), ToolStatus: ToolStatusSuccess},
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
	records := []MergedRecord{
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
	records := []MergedRecord{
		rec("s1", "a", apmconfig.EventAgentSpawn, "", 0),
		{SessionID: "s1", Agent: "a", Kind: RecordKindAssistantText, AssistantText: "The task is complete.", CreatedAt: baseTime.Add(5 * time.Minute)},
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

var writeRecCounter atomic.Int32

func writeRec(session, agent, path, command string, offset time.Duration) MergedRecord {
	id := writeRecCounter.Add(1)
	input, _ := json.Marshal(map[string]string{"command": command, "path": path, "content": "x"})
	return MergedRecord{
		SessionID: session,
		Agent:     agent,
		Kind:      RecordKindToolUse,
		ToolUseID: fmt.Sprintf("tu-write-%d", id),
		ToolName:  "write",
		ToolInput: input,
		PreToolTs: baseTime.Add(offset),
		Cwd:       "/tmp",
	}
}

func TestFilesChangedSessionMetric(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []MergedRecord{
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
	records := []MergedRecord{
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
	records := []MergedRecord{
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
	records := []MergedRecord{
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

// --- Task 4 (token/credit): AggregateDetail populates token/credit fields ---

// TestAggregateDetail_TokenCredits verifies that a "sessionMeta" record is
// consumed by AggregateDetail and its values appear in SessionMetric and
// AgentMetric.
func TestAggregateDetail_TokenCredits(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "coder", Kind: RecordKindSessionMeta,
			TotalInputTokens: 400, TotalOutputTokens: 600, TotalCredits: 2.0},
		rec("s1", "coder", apmconfig.EventUserPromptSubmit, "", 1*time.Minute),
	}
	d := mustAggregate(t, records, now)

	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(d.Sessions))
	}
	sm := d.Sessions[0].SessionMetric
	if sm.TotalInputTokens != 400 {
		t.Errorf("SessionMetric.TotalInputTokens: want 400, got %d", sm.TotalInputTokens)
	}
	if sm.TotalOutputTokens != 600 {
		t.Errorf("SessionMetric.TotalOutputTokens: want 600, got %d", sm.TotalOutputTokens)
	}
	if sm.TotalCredits != 2.0 {
		t.Errorf("SessionMetric.TotalCredits: want 2.0, got %f", sm.TotalCredits)
	}

	byName := map[string]AgentMetric{}
	for _, a := range d.Overview.Agents {
		byName[a.Name] = a
	}
	am := byName["coder"]
	if am.TotalInputTokens != 400 {
		t.Errorf("AgentMetric.TotalInputTokens: want 400, got %d", am.TotalInputTokens)
	}
	if am.TotalOutputTokens != 600 {
		t.Errorf("AgentMetric.TotalOutputTokens: want 600, got %d", am.TotalOutputTokens)
	}
	if am.TotalCredits != 2.0 {
		t.Errorf("AgentMetric.TotalCredits: want 2.0, got %f", am.TotalCredits)
	}
}

// TestAggregateDetail_TokenCredits_MultiSession verifies that AgentMetric
// accumulates token/credit totals across multiple sessions.
func TestAggregateDetail_TokenCredits_MultiSession(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []MergedRecord{
		{SessionID: "s1", Agent: "coder", Kind: RecordKindSessionMeta,
			TotalInputTokens: 100, TotalOutputTokens: 200, TotalCredits: 1.0},
		rec("s1", "coder", apmconfig.EventUserPromptSubmit, "", 1*time.Minute),
		{SessionID: "s2", Agent: "coder", Kind: RecordKindSessionMeta,
			TotalInputTokens: 300, TotalOutputTokens: 400, TotalCredits: 1.5},
		rec("s2", "coder", apmconfig.EventUserPromptSubmit, "", 30*time.Minute),
	}
	d := mustAggregate(t, records, now)

	byName := map[string]AgentMetric{}
	for _, a := range d.Overview.Agents {
		byName[a.Name] = a
	}
	am := byName["coder"]
	if am.TotalInputTokens != 400 {
		t.Errorf("AgentMetric.TotalInputTokens: want 400, got %d", am.TotalInputTokens)
	}
	if am.TotalOutputTokens != 600 {
		t.Errorf("AgentMetric.TotalOutputTokens: want 600, got %d", am.TotalOutputTokens)
	}
	if am.TotalCredits != 2.5 {
		t.Errorf("AgentMetric.TotalCredits: want 2.5, got %f", am.TotalCredits)
	}
}

// TestAggregateDetail_TokenCredits_Zero verifies that sessions with no
// sessionMeta record have zero token/credit fields.
func TestAggregateDetail_TokenCredits_Zero(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(1 * time.Hour)
	records := []MergedRecord{
		rec("s1", "coder", apmconfig.EventUserPromptSubmit, "", 1*time.Minute),
	}
	d := mustAggregate(t, records, now)

	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(d.Sessions))
	}
	sm := d.Sessions[0].SessionMetric
	if sm.TotalInputTokens != 0 || sm.TotalOutputTokens != 0 || sm.TotalCredits != 0 {
		t.Errorf("expected zero token/credit fields, got in=%d out=%d credits=%f",
			sm.TotalInputTokens, sm.TotalOutputTokens, sm.TotalCredits)
	}
}

func TestAggregateDetail_CancelledCtx(t *testing.T) {
	t.Parallel()
	records := []MergedRecord{
		rec("s1", "agent1", apmconfig.EventUserPromptSubmit, "", 0),
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

// --- Task 6: IDE session display integration --------------------------------

// TestTouchSessionState_UpdatedAt verifies that UpdatedAt advances s.end so
// that Duration > 0 for IDE sessionMeta records.
func TestTouchSessionState_UpdatedAt(t *testing.T) {
	t.Parallel()
	createdAt := baseTime
	updatedAt := baseTime.Add(30 * time.Minute)
	r := MergedRecord{
		SessionID: "ide-1",
		Agent:     "kiro-ide",
		Kind:      RecordKindSessionMeta,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	st := newAggState(1, baseTime.Add(time.Hour))
	s := touchSessionState(st, r)
	if !s.end.Equal(updatedAt) {
		t.Errorf("s.end = %v, want %v (UpdatedAt)", s.end, updatedAt)
	}
	dur := s.end.Sub(s.start)
	if dur <= 0 {
		t.Errorf("Duration = %v, want > 0", dur)
	}
}

// TestAggregateDetail_IDESession verifies that an IDE sessionMeta MergedRecord
// produces a SessionDetail with Agent="kiro-ide", TotalCredits > 0, Duration > 0.
func TestAggregateDetail_IDESession(t *testing.T) {
	t.Parallel()
	createdAt := baseTime
	updatedAt := baseTime.Add(28 * time.Minute)
	now := baseTime.Add(1 * time.Hour)
	records := []MergedRecord{
		{
			SessionID:    "ide-sess-1",
			Kind:         RecordKindSessionMeta,
			Agent:        "kiro-ide",
			Title:        "Implement feature X",
			Cwd:          "/home/user/project-alpha",
			CreatedAt:    createdAt,
			UpdatedAt:    updatedAt,
			TotalCredits: 0.125,
		},
	}
	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(d.Sessions))
	}
	sm := d.Sessions[0].SessionMetric
	if sm.Agent != "kiro-ide" {
		t.Errorf("Agent: want kiro-ide, got %q", sm.Agent)
	}
	if sm.TotalCredits != 0.125 {
		t.Errorf("TotalCredits: want 0.125, got %f", sm.TotalCredits)
	}
	if sm.Duration <= 0 {
		t.Errorf("Duration: want > 0, got %v", sm.Duration)
	}
	if sm.ToolCalls != 0 {
		t.Errorf("ToolCalls: want 0, got %d", sm.ToolCalls)
	}
	if sm.Prompts != 0 {
		t.Errorf("Prompts: want 0, got %d", sm.Prompts)
	}
	if sm.FilesChanged != 0 {
		t.Errorf("FilesChanged: want 0, got %d", sm.FilesChanged)
	}
}

// TestAggregateDetail_MixedSources verifies that CLI and IDE sessions coexist
// correctly in the same aggregation.
func TestAggregateDetail_MixedSources(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(2 * time.Hour)
	records := []MergedRecord{
		// CLI session
		rec("cli-1", "coder", apmconfig.EventUserPromptSubmit, "", 1*time.Minute),
		{SessionID: "cli-1", Agent: "coder", Kind: RecordKindSessionMeta,
			TotalInputTokens: 100, TotalOutputTokens: 200, TotalCredits: 1.0,
			CreatedAt: baseTime, UpdatedAt: baseTime.Add(5 * time.Minute)},
		// IDE session
		{
			SessionID:    "ide-1",
			Kind:         RecordKindSessionMeta,
			Agent:        "kiro-ide",
			Cwd:          "/home/user/proj",
			CreatedAt:    baseTime.Add(10 * time.Minute),
			UpdatedAt:    baseTime.Add(40 * time.Minute),
			TotalCredits: 0.5,
		},
	}
	d := mustAggregate(t, records, now)
	if len(d.Sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(d.Sessions))
	}
	byAgent := map[string]SessionMetric{}
	for _, sd := range d.Sessions {
		byAgent[sd.Agent] = sd.SessionMetric
	}
	cli := byAgent["coder"]
	if cli.TotalCredits != 1.0 {
		t.Errorf("CLI TotalCredits: want 1.0, got %f", cli.TotalCredits)
	}
	ide := byAgent["kiro-ide"]
	if ide.TotalCredits != 0.5 {
		t.Errorf("IDE TotalCredits: want 0.5, got %f", ide.TotalCredits)
	}
	if ide.Duration <= 0 {
		t.Errorf("IDE Duration: want > 0, got %v", ide.Duration)
	}
}
