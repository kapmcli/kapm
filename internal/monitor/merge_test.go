package monitor

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func ts(min int) time.Time {
	return time.Date(2026, 4, 22, 10, min, 0, 0, time.UTC)
}

func TestMergeSessionDetails(t *testing.T) {
	type want struct {
		id, agent, agentKey, title, cwd string
		start, end                      time.Time
		active                          bool
		toolCalls, prompts              int
		timelineTs                      []time.Time
		promptOrder                     []string
		refs                            []AgentRef
	}
	tests := []struct {
		name  string
		input []SessionDetail
		want  want
	}{
		{
			name:  "empty input",
			input: nil,
			want:  want{},
		},
		{
			name: "two agents same sid",
			input: []SessionDetail{
				{
					SessionMetric: SessionMetric{
						ID: "sid1", AgentKey: "sid1|orchestrator", Agent: "orchestrator",
						Title: "first prompt from orch", Cwd: "/old",
						StartTime: ts(0), EndTime: ts(10), LastActivity: ts(10),
						Active: false, ToolCalls: 2, Prompts: 1,
					},
					PromptHistory: []string{"first prompt from orch"},
					Timeline: []EventEntry{
						{Ts: ts(0), Event: "userPromptSubmit"},
						{Ts: ts(5), Event: "preToolUse", Tool: "read"},
						{Ts: ts(6), Event: "postToolUse", Tool: "read"},
						{Ts: ts(10), Event: "stop"},
					},
				},
				{
					SessionMetric: SessionMetric{
						ID: "sid1", AgentKey: "sid1|lead", Agent: "lead",
						Title: "lead prompt", Cwd: "/new",
						StartTime: ts(20), EndTime: ts(30), LastActivity: ts(30),
						Active: true, ToolCalls: 3, Prompts: 2,
					},
					PromptHistory: []string{"lead prompt 2", "lead prompt 1"},
					Timeline: []EventEntry{
						{Ts: ts(20), Event: "userPromptSubmit"},
						{Ts: ts(25), Event: "userPromptSubmit"},
						{Ts: ts(28), Event: "preToolUse", Tool: "bash"},
					},
				},
			},
			want: want{
				id: "sid1", agent: "(all)", agentKey: "sid1|(all)",
				title: "lead prompt", cwd: "/new",
				start: ts(0), end: ts(30), active: true,
				toolCalls: 5, prompts: 3,
				timelineTs: []time.Time{ts(0), ts(5), ts(6), ts(10), ts(20), ts(25), ts(28)},
				// Newest first by event ts: ts(25) lead-2 → ts(20) lead-1 → ts(0) orch
				promptOrder: []string{"lead prompt 2", "lead prompt 1", "first prompt from orch"},
				refs: []AgentRef{
					{Agent: "orchestrator", AgentKey: "sid1|orchestrator"},
					{Agent: "lead", AgentKey: "sid1|lead"},
				},
			},
		},
		{
			name: "ignores mismatched IDs",
			input: []SessionDetail{
				{SessionMetric: SessionMetric{ID: "sid1", Agent: "a", AgentKey: "sid1|a", StartTime: ts(0), EndTime: ts(5), LastActivity: ts(5)}},
				{SessionMetric: SessionMetric{ID: "other", Agent: "b", AgentKey: "other|b", StartTime: ts(0), EndTime: ts(100), LastActivity: ts(100)}},
			},
			want: want{
				id: "sid1", agent: "(all)", agentKey: "sid1|(all)",
				start: ts(0), end: ts(5),
				refs: []AgentRef{{Agent: "a", AgentKey: "sid1|a"}},
			},
		},
		{
			name: "title falls back to most recent non-empty",
			input: []SessionDetail{
				{SessionMetric: SessionMetric{ID: "sid", Agent: "a", AgentKey: "sid|a", Title: "", StartTime: ts(0), EndTime: ts(10), LastActivity: ts(10)}},
				{SessionMetric: SessionMetric{ID: "sid", Agent: "b", AgentKey: "sid|b", Title: "earlier title", StartTime: ts(0), EndTime: ts(5), LastActivity: ts(5)}},
			},
			want: want{
				id: "sid", agent: "(all)", agentKey: "sid|(all)",
				title: "earlier title",
				start: ts(0), end: ts(10),
				refs: []AgentRef{
					{Agent: "a", AgentKey: "sid|a"},
					{Agent: "b", AgentKey: "sid|b"},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			merged, refs := MergeSessionDetails(tc.input)
			if tc.want.id == "" && len(tc.input) == 0 {
				if merged.ID != "" || len(refs) != 0 {
					t.Fatalf("empty input: want zero, got %+v refs=%v", merged, refs)
				}
				return
			}
			if merged.ID != tc.want.id {
				t.Errorf("ID = %q, want %q", merged.ID, tc.want.id)
			}
			if merged.Agent != tc.want.agent {
				t.Errorf("Agent = %q, want %q", merged.Agent, tc.want.agent)
			}
			if merged.AgentKey != tc.want.agentKey {
				t.Errorf("AgentKey = %q, want %q", merged.AgentKey, tc.want.agentKey)
			}
			if merged.Title != tc.want.title {
				t.Errorf("Title = %q, want %q", merged.Title, tc.want.title)
			}
			if tc.want.cwd != "" && merged.Cwd != tc.want.cwd {
				t.Errorf("Cwd = %q, want %q", merged.Cwd, tc.want.cwd)
			}
			if !merged.StartTime.Equal(tc.want.start) {
				t.Errorf("StartTime = %v, want %v", merged.StartTime, tc.want.start)
			}
			if !merged.EndTime.Equal(tc.want.end) {
				t.Errorf("EndTime = %v, want %v", merged.EndTime, tc.want.end)
			}
			if merged.Active != tc.want.active {
				t.Errorf("Active = %v, want %v", merged.Active, tc.want.active)
			}
			if tc.want.toolCalls > 0 && merged.ToolCalls != tc.want.toolCalls {
				t.Errorf("ToolCalls = %d, want %d", merged.ToolCalls, tc.want.toolCalls)
			}
			if tc.want.prompts > 0 && merged.Prompts != tc.want.prompts {
				t.Errorf("Prompts = %d, want %d", merged.Prompts, tc.want.prompts)
			}
			wantDur := JSONDuration(tc.want.end.Sub(tc.want.start))
			if merged.Duration != wantDur {
				t.Errorf("Duration = %v, want %v", merged.Duration, wantDur)
			}
			if len(tc.want.timelineTs) > 0 {
				if len(merged.Timeline) != len(tc.want.timelineTs) {
					t.Fatalf("Timeline len = %d, want %d", len(merged.Timeline), len(tc.want.timelineTs))
				}
				for i, want := range tc.want.timelineTs {
					if !merged.Timeline[i].Ts.Equal(want) {
						t.Errorf("Timeline[%d].Ts = %v, want %v", i, merged.Timeline[i].Ts, want)
					}
				}
				if !slices.IsSortedFunc(merged.Timeline, func(a, b EventEntry) int { return a.Ts.Compare(b.Ts) }) {
					t.Errorf("Timeline not sorted ascending")
				}
			}
			if len(tc.want.promptOrder) > 0 {
				if !slices.Equal(merged.PromptHistory, tc.want.promptOrder) {
					t.Errorf("PromptHistory = %v, want %v", merged.PromptHistory, tc.want.promptOrder)
				}
			}
			if !slices.Equal(refs, tc.want.refs) {
				t.Errorf("refs = %v, want %v", refs, tc.want.refs)
			}
		})
	}
}

func TestMergeToolSummary(t *testing.T) {
	// Agent A: bash 3 calls (1 error, avg 2s), read 2 calls (0 errors, avg 1s)
	// Agent B: bash 2 calls (0 errors, avg 4s), write 1 call (1 error, avg 0)
	// Merged bash: 5 calls, 1 error, successRate=0.8, avgDuration weighted by successCount
	//   successCount_A=2, successCount_B=2 → total=4
	//   avgDur = (2s*2 + 4s*2) / 4 = 3s
	// Merged read: 2 calls, 0 errors, successRate=1.0, avgDur=1s
	// Merged write: 1 call, 1 error, successRate=0.0, avgDur=0
	// Sort by CallCount desc: bash(5), read(2), write(1)
	dur := func(d time.Duration) JSONDuration { return JSONDuration(d) }
	details := []SessionDetail{
		{
			SessionMetric: SessionMetric{ID: "s1", Agent: "a", AgentKey: "s1|a", StartTime: ts(0), EndTime: ts(10), LastActivity: ts(10)},
			ToolSummary: []SessionToolSummary{
				{Tool: "bash", CallCount: 3, ErrorCount: 1, SuccessRate: 2.0 / 3.0, AvgDuration: dur(2 * time.Second)},
				{Tool: "read", CallCount: 2, ErrorCount: 0, SuccessRate: 1.0, AvgDuration: dur(time.Second)},
			},
		},
		{
			SessionMetric: SessionMetric{ID: "s1", Agent: "b", AgentKey: "s1|b", StartTime: ts(0), EndTime: ts(10), LastActivity: ts(10)},
			ToolSummary: []SessionToolSummary{
				{Tool: "bash", CallCount: 2, ErrorCount: 0, SuccessRate: 1.0, AvgDuration: dur(4 * time.Second)},
				{Tool: "write", CallCount: 1, ErrorCount: 1, SuccessRate: 0.0, AvgDuration: 0},
			},
		},
	}
	merged, _ := MergeSessionDetails(details)
	ts_ := merged.ToolSummary
	if len(ts_) != 3 {
		t.Fatalf("ToolSummary len = %d, want 3", len(ts_))
	}
	// Sorted by CallCount desc: bash, read, write
	if ts_[0].Tool != "bash" || ts_[1].Tool != "read" || ts_[2].Tool != "write" {
		t.Errorf("order = %v/%v/%v, want bash/read/write", ts_[0].Tool, ts_[1].Tool, ts_[2].Tool)
	}
	bash := ts_[0]
	if bash.CallCount != 5 {
		t.Errorf("bash CallCount = %d, want 5", bash.CallCount)
	}
	if bash.ErrorCount != 1 {
		t.Errorf("bash ErrorCount = %d, want 1", bash.ErrorCount)
	}
	wantRate := 4.0 / 5.0
	if bash.SuccessRate < wantRate-0.001 || bash.SuccessRate > wantRate+0.001 {
		t.Errorf("bash SuccessRate = %f, want %f", bash.SuccessRate, wantRate)
	}
	wantAvg := JSONDuration(3 * time.Second)
	if bash.AvgDuration != wantAvg {
		t.Errorf("bash AvgDuration = %v, want %v", bash.AvgDuration, wantAvg)
	}
	read := ts_[1]
	if read.CallCount != 2 || read.ErrorCount != 0 {
		t.Errorf("read counts = %d/%d, want 2/0", read.CallCount, read.ErrorCount)
	}
	write := ts_[2]
	if write.CallCount != 1 || write.ErrorCount != 1 {
		t.Errorf("write counts = %d/%d, want 1/1", write.CallCount, write.ErrorCount)
	}
	if write.SuccessRate != 0.0 {
		t.Errorf("write SuccessRate = %f, want 0", write.SuccessRate)
	}
}

func TestMergeAssistantResponse(t *testing.T) {
	details := []SessionDetail{
		{
			SessionMetric:     SessionMetric{ID: "s1", Agent: "alpha", AgentKey: "s1|alpha", StartTime: ts(0), EndTime: ts(5), LastActivity: ts(5)},
			AssistantResponse: "hello from alpha",
		},
		{
			SessionMetric:     SessionMetric{ID: "s1", Agent: "beta", AgentKey: "s1|beta", StartTime: ts(0), EndTime: ts(5), LastActivity: ts(5)},
			AssistantResponse: "hello from beta",
		},
	}
	merged, _ := MergeSessionDetails(details)
	got := merged.AssistantResponse
	if !strings.Contains(got, "[alpha]\nhello from alpha") {
		t.Errorf("missing alpha prefix in %q", got)
	}
	if !strings.Contains(got, "[beta]\nhello from beta") {
		t.Errorf("missing beta prefix in %q", got)
	}
	if !strings.Contains(got, "\n\n---\n\n") {
		t.Errorf("missing separator in %q", got)
	}
	if len(got) > 2048 {
		t.Errorf("response length %d exceeds 2048", len(got))
	}
}

func TestMergeAssistantResponseSingle(t *testing.T) {
	details := []SessionDetail{
		{
			SessionMetric:     SessionMetric{ID: "s1", Agent: "solo", AgentKey: "s1|solo", StartTime: ts(0), EndTime: ts(5), LastActivity: ts(5)},
			AssistantResponse: "only response",
		},
	}
	merged, _ := MergeSessionDetails(details)
	if merged.AssistantResponse != "only response" {
		t.Errorf("single agent: got %q, want %q", merged.AssistantResponse, "only response")
	}
}


