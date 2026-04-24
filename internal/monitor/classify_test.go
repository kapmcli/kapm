package monitor

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

func TestClassifyShell(t *testing.T) {
	cases := []struct {
		name  string
		input string
		cwd   string
		want  string
	}{
		{"git subcommand kept", `{"command":"git push -u origin main"}`, "/w", "shell:git push"},
		{"go subcommand kept", `{"command":"go test ./..."}`, "/w", "shell:go test"},
		{"cd to cwd then git", `{"command":"cd /w && git status"}`, "/w", "shell:git status"},
		{"non-allowlisted collapses to top", `{"command":"ls -la"}`, "/w", "shell:ls"},
		{"grep not in allowlist", `{"command":"grep -r foo ."}`, "/w", "shell:grep"},
		{"sudo skipped", `{"command":"sudo rm -rf /tmp/x"}`, "/w", "shell:rm"},
		{"env-var prefix bails out", `{"command":"FOO=bar cmd"}`, "/w", "shell"},
		{"empty command", `{"command":""}`, "/w", "shell"},
		{"unparsable JSON", `not json`, "/w", "shell"},
		{"allowlisted with flag-only second token", `{"command":"git --help"}`, "/w", "shell:git"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyShell(json.RawMessage(c.input), c.cwd)
			if got != c.want {
				t.Errorf("classifyShell(%q) = %q; want %q", c.input, got, c.want)
			}
		})
	}
}

// TestAggregateShellSplitsIntoDerivedBuckets ensures that concurrent shell
// invocations with different commands are counted under distinct keys so an
// "ls 100% success" and a "git push 50% failure" don't get averaged together.
func TestAggregateShellSplitsIntoDerivedBuckets(t *testing.T) {
	baseTime := time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC)
	records := []Record{
		// ls: success
		{Ts: baseTime, Session: "s", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "shell", Cwd: "/w",
			ToolInput: json.RawMessage(`{"command":"ls -la"}`)},
		{Ts: baseTime.Add(1 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "shell",
			ToolInput:    json.RawMessage(`{"command":"ls -la"}`),
			ToolResponse: json.RawMessage(`{"items":[{"Json":{"exit_status":"exit status: 0"}}]}`)},

		// git push: failure (exit 1)
		{Ts: baseTime.Add(2 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "shell", Cwd: "/w",
			ToolInput: json.RawMessage(`{"command":"git push -u origin main"}`)},
		{Ts: baseTime.Add(3 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "shell",
			ToolInput:    json.RawMessage(`{"command":"git push -u origin main"}`),
			ToolResponse: json.RawMessage(`{"items":[{"Json":{"exit_status":"exit status: 1"}}]}`)},

		// git push: success
		{Ts: baseTime.Add(4 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "shell", Cwd: "/w",
			ToolInput: json.RawMessage(`{"command":"git push -u origin main"}`)},
		{Ts: baseTime.Add(5 * time.Second), Session: "s", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "shell",
			ToolInput:    json.RawMessage(`{"command":"git push -u origin main"}`),
			ToolResponse: json.RawMessage(`{"items":[{"Json":{"exit_status":"exit status: 0"}}]}`)},
	}

	d := AggregateDetail(records, baseTime.Add(time.Hour))

	byName := map[string]*ToolDetail{}
	for i := range d.Tools {
		byName[d.Tools[i].Name] = &d.Tools[i]
	}

	if _, ok := byName["shell"]; ok {
		t.Errorf("derived bucketing should not leave a plain %q entry", "shell")
	}
	ls := byName["shell:ls"]
	if ls == nil || ls.CallCount != 1 || ls.ErrorCount != 0 {
		t.Errorf("shell:ls: want 1 call / 0 errors, got %+v", ls)
	}
	gp := byName["shell:git push"]
	if gp == nil || gp.CallCount != 2 || gp.ErrorCount != 1 {
		t.Errorf("shell:git push: want 2 calls / 1 error, got %+v", gp)
	}
}
