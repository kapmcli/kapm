package monitor

import (
	"encoding/json"
	"testing"
	"time"
)

func TestClassifyShell(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		cwd   string
		want  string
	}{
		{"git subcommand kept", `{"command":"git push -u origin main"}`, "/w", "shell:git push"},
		{"go subcommand kept", `{"command":"go test ./..."}`, "/w", "shell:go test"},
		{"npx package command kept", `{"command":"npx defuddle parse https://example.com --markdown"}`, "/w", "shell:npx defuddle"},
		{"npx scoped package kept", `{"command":"npx @playwright/cli@latest screenshot page.png"}`, "/w", "shell:npx @playwright/cli@latest"},
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
			t.Parallel()
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
	t.Parallel()
	baseTime := time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC)
	records := []MergedRecord{
		// ls: success
		{SessionID: "s", Agent: "a", Kind: "toolUse", ToolName: "shell", Cwd: "/w",
			ToolUseID: "tu-ls", PreToolTs: baseTime, ToolInput: json.RawMessage(`{"command":"ls -la"}`)},
		{SessionID: "s", Agent: "a", Kind: "toolResult", ToolName: "shell",
			ToolUseID: "tu-ls", PostToolTs: baseTime.Add(1 * time.Second), ToolStatus: "success"},

		// git push: failure
		{SessionID: "s", Agent: "a", Kind: "toolUse", ToolName: "shell", Cwd: "/w",
			ToolUseID: "tu-gp1", PreToolTs: baseTime.Add(2 * time.Second), ToolInput: json.RawMessage(`{"command":"git push -u origin main"}`)},
		{SessionID: "s", Agent: "a", Kind: "toolResult", ToolName: "shell",
			ToolUseID: "tu-gp1", PostToolTs: baseTime.Add(3 * time.Second), ToolStatus: "error", ErrorDetail: "exit 1"},

		// git push: success
		{SessionID: "s", Agent: "a", Kind: "toolUse", ToolName: "shell", Cwd: "/w",
			ToolUseID: "tu-gp2", PreToolTs: baseTime.Add(4 * time.Second), ToolInput: json.RawMessage(`{"command":"git push -u origin main"}`)},
		{SessionID: "s", Agent: "a", Kind: "toolResult", ToolName: "shell",
			ToolUseID: "tu-gp2", PostToolTs: baseTime.Add(5 * time.Second), ToolStatus: "success"},
	}

	d := mustAggregate(t, records, baseTime.Add(time.Hour))

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
