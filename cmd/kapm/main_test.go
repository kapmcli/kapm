package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/install"
	"github.com/kapmcli/kapm/internal/power"
)

func TestRunUsageAndHelp(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no args", args: nil},
		{name: "help command", args: []string{"help"}},
		{name: "long help flag", args: []string{"--help"}},
		{name: "short help flag", args: []string{"-h"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := captureOutput(t, func() error {
				return run(tt.args)
			})
			if err != nil {
				t.Fatalf("run(%v) error = %v", tt.args, err)
			}
			if !strings.Contains(stdout, "Usage:") {
				t.Fatalf("stdout = %q, want usage text", stdout)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
		})
	}
}

func TestRunUnknownCommand(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return run([]string{"bogus"})
	})
	if err == nil {
		t.Fatal("run() error = nil, want unknown command error")
	}
	if !strings.Contains(err.Error(), `unknown command "bogus"`) {
		t.Fatalf("run() error = %v, want unknown command error", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "Available commands:") {
		t.Fatalf("stderr = %q, want usage text", stderr)
	}
}

func TestRunAgentUsageAndHelp(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "agent no subcommand", args: []string{"agent"}},
		{name: "agent help", args: []string{"agent", "help"}},
		{name: "agent long help", args: []string{"agent", "--help"}},
		{name: "agent short help", args: []string{"agent", "-h"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := captureOutput(t, func() error {
				return run(tt.args)
			})
			if err != nil {
				t.Fatalf("run(%v) error = %v", tt.args, err)
			}
			if !strings.Contains(stdout, "Usage: kapm agent <subcommand>") {
				t.Fatalf("stdout = %q, want agent usage text", stdout)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
		})
	}
}

func TestRunCommandHelpOutputsToStdout(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "sync help", args: []string{"sync", "--help"}, want: "Usage: kapm sync [flags]"},
		{name: "monitor help", args: []string{"monitor", "--help"}, want: "Usage: kapm monitor [flags]"},
		{name: "serve help", args: []string{"serve", "--help"}, want: "Usage: kapm serve [flags]"},
		{name: "init-hook help", args: []string{"init-hook", "--help"}, want: "Usage: kapm init-hook [flags]"},
		{name: "init-ide-hook help", args: []string{"init-ide-hook", "--help"}, want: "Usage: kapm init-ide-hook [flags]"},
		{name: "hook-handler help", args: []string{"hook-handler", "--help"}, want: "Usage: kapm hook-handler [flags]"},
		{name: "ide-hook-handler help", args: []string{"ide-hook-handler", "--help"}, want: "Usage: kapm ide-hook-handler [flags]"},
		{name: "power help", args: []string{"power", "--help"}, want: "Usage: kapm power <subcommand>"},
		{name: "power install help", args: []string{"power", "install", "--help"}, want: "Usage: kapm power install <url-or-path> [flags]"},
		{name: "agent generate help", args: []string{"agent", "generate", "--help"}, want: "Usage: kapm agent generate [flags]"},
		{name: "agent update help", args: []string{"agent", "update", "--help"}, want: "Usage: kapm agent update <name> [flags]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := captureOutput(t, func() error {
				return run(tt.args)
			})
			if err != nil {
				t.Fatalf("run(%v) error = %v", tt.args, err)
			}
			if !strings.Contains(stdout, tt.want) {
				t.Fatalf("stdout = %q, want usage text containing %q", stdout, tt.want)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
		})
	}
}

func TestRunArgumentValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "sync rejects positional args",
			args:    []string{"sync", "extra"},
			wantErr: "sync does not accept positional arguments",
		},
		{
			name:    "monitor rejects positional args",
			args:    []string{"monitor", "extra"},
			wantErr: "monitor does not accept positional arguments",
		},
		{
			name:    "serve rejects positional args",
			args:    []string{"serve", "extra"},
			wantErr: "serve does not accept positional arguments",
		},
		{
			name:    "agent generate rejects positional args",
			args:    []string{"agent", "generate", "extra"},
			wantErr: "agent generate does not accept positional arguments",
		},
		{
			name:    "agent update requires one argument",
			args:    []string{"agent", "update"},
			wantErr: "agent update requires exactly one argument: <name>",
		},
		{
			name:    "agent rejects unknown subcommand",
			args:    []string{"agent", "bogus"},
			wantErr: `unknown agent subcommand "bogus"`,
		},
		{
			name:    "power rejects unknown subcommand",
			args:    []string{"power", "bogus"},
			wantErr: `unknown power subcommand "bogus"`,
		},
		{
			name:    "power install requires source",
			args:    []string{"power", "install"},
			wantErr: "power install requires exactly one argument",
		},
		{
			name:    "init-hook rejects positional args",
			args:    []string{"init-hook", "extra"},
			wantErr: "init-hook does not accept positional arguments",
		},
		{
			name:    "init-ide-hook rejects positional args",
			args:    []string{"init-ide-hook", "extra"},
			wantErr: "init-ide-hook does not accept positional arguments",
		},
		{
			name:    "hook-handler rejects positional args",
			args:    []string{"hook-handler", "extra"},
			wantErr: "hook-handler does not accept positional arguments",
		},
		{
			name:    "ide-hook-handler rejects positional args",
			args:    []string{"ide-hook-handler", "extra"},
			wantErr: "ide-hook-handler does not accept positional arguments",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := captureOutput(t, func() error {
				return run(tt.args)
			})
			if err == nil {
				t.Fatalf("run(%v) error = nil, want %q", tt.args, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("run(%v) error = %v, want substring %q", tt.args, err, tt.wantErr)
			}
		})
	}
}

func TestRunMonitorJSONFilterValidationDoesNotPrintTwice(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return run([]string{"monitor", "--session", "abc"})
	})
	if err == nil {
		t.Fatal("run(monitor --session) error = nil, want non-nil error")
	}
	if !strings.Contains(err.Error(), "--session and --agent require --json") {
		t.Fatalf("error = %v, want json filter validation error", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty (caller should print once)", stderr)
	}
}

func TestSplitInstallArgs(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		wantForce       bool
		wantTargetDir   string
		wantInstallArgs []string
	}{
		{
			name:            "passes through apm flags",
			args:            []string{"--update", "owner/repo"},
			wantTargetDir:   ".",
			wantInstallArgs: []string{"--update", "owner/repo"},
		},
		{
			name:            "extracts sync force",
			args:            []string{"--sync-force", "--update", "owner/repo"},
			wantForce:       true,
			wantTargetDir:   ".",
			wantInstallArgs: []string{"--update", "owner/repo"},
		},
		{
			name:            "plain force is forwarded to apm",
			args:            []string{"--force", "owner/repo"},
			wantTargetDir:   ".",
			wantInstallArgs: []string{"--force", "owner/repo"},
		},
		{
			name:            "install help is forwarded to apm",
			args:            []string{"--sync-force", "--help"},
			wantForce:       true,
			wantTargetDir:   ".",
			wantInstallArgs: []string{"--help"},
		},
		{
			name:            "extracts target-dir",
			args:            []string{"--target-dir", "/tmp/x", "owner/repo"},
			wantTargetDir:   "/tmp/x",
			wantInstallArgs: []string{"owner/repo"},
		},
		{
			name:            "extracts target-dir equals form",
			args:            []string{"--target-dir=/tmp/y", "owner/repo"},
			wantTargetDir:   "/tmp/y",
			wantInstallArgs: []string{"owner/repo"},
		},
		{
			name:            "single-dash target-dir is forwarded to apm",
			args:            []string{"-target-dir=/tmp/y", "owner/repo"},
			wantTargetDir:   ".",
			wantInstallArgs: []string{"-target-dir=/tmp/y", "owner/repo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotForce, gotTargetDir, gotInstallArgs := splitInstallArgs(tt.args)
			if gotForce != tt.wantForce {
				t.Fatalf("splitInstallArgs(%v) force = %v, want %v", tt.args, gotForce, tt.wantForce)
			}
			if gotTargetDir != tt.wantTargetDir {
				t.Fatalf("splitInstallArgs(%v) targetDir = %q, want %q", tt.args, gotTargetDir, tt.wantTargetDir)
			}
			if !reflect.DeepEqual(gotInstallArgs, tt.wantInstallArgs) {
				t.Fatalf("splitInstallArgs(%v) install args = %v, want %v", tt.args, gotInstallArgs, tt.wantInstallArgs)
			}
		})
	}
}

func TestRunInstallForwardsApmArgs(t *testing.T) {
	origInstallRun := installRun
	t.Cleanup(func() {
		installRun = origInstallRun
	})

	var got install.Options
	installRun = func(opts install.Options) error {
		got = opts
		return nil
	}

	if err := run([]string{"install", "--sync-force", "--update", "owner/repo"}); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	if !got.Force {
		t.Fatal("install options Force = false, want true")
	}
	if got.Root != "." {
		t.Fatalf("install options Root = %q, want .", got.Root)
	}
	if !reflect.DeepEqual(got.InstallArgs, []string{"--update", "owner/repo"}) {
		t.Fatalf("install args = %v, want %v", got.InstallArgs, []string{"--update", "owner/repo"})
	}
}

func TestRunPowerInstallCallsPowerInstaller(t *testing.T) {
	orig := powerInstallRun
	t.Cleanup(func() { powerInstallRun = orig })

	target := t.TempDir()
	powerDir := filepath.Join(target, ".kiro", "powers", "sample-power")
	if err := os.MkdirAll(filepath.Join(powerDir, "hooks"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(powerDir, "steering"), 0o755); err != nil {
		t.Fatalf("MkdirAll(steering) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(powerDir, "POWER.md"), []byte("---\nname: sample-power\ndescription: test\n---\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(POWER.md) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(powerDir, "steering", "style.md"), []byte("# style\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(steering) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(powerDir, "mcp.json"), []byte("{\"mcpServers\":{\"fetch\":{\"type\":\"http\",\"url\":\"https://example.com/mcp\"}}}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(mcp.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(powerDir, "hooks", "agent-spawn.kiro.hook"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(hook) error = %v", err)
	}

	var got power.InstallOptions
	powerInstallRun = func(_ context.Context, opts power.InstallOptions) (*power.Result, error) {
		got = opts
		return &power.Result{
			Name:          "sample-power",
			PowerDir:      powerDir,
			ResourcePaths: []string{filepath.Join(powerDir, "POWER.md"), filepath.Join(powerDir, "steering", "style.md")},
			MCPConfigPath: filepath.Join(powerDir, "mcp.json"),
			HooksDir:      filepath.Join(powerDir, "hooks"),
		}, nil
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return run([]string{"power", "install", "./testdata/power/sample-power/input", "--target-dir", target, "--timeout", "7s"})
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if got.Source.Kind != power.SourceLocal || got.Source.Path != "./testdata/power/sample-power/input" {
		t.Fatalf("source = %#v", got.Source)
	}
	if got.TargetDir != target {
		t.Fatalf("TargetDir = %q, want %q", got.TargetDir, target)
	}
	if got.Timeout != 7*time.Second {
		t.Fatalf("Timeout = %v, want 7s", got.Timeout)
	}
	if !strings.Contains(stdout, `Installed power "sample-power"`) {
		t.Fatalf("stdout = %q, want install summary", stdout)
	}
	if !strings.Contains(stdout, "Suggested custom agent config:") {
		t.Fatalf("stdout = %q, want suggestion header", stdout)
	}
	for _, resourcePath := range []string{filepath.Join(powerDir, "POWER.md"), filepath.Join(powerDir, "steering", "style.md")} {
		if !strings.Contains(stdout, fmt.Sprintf(`"file://%s"`, filepath.ToSlash(resourcePath))) {
			t.Fatalf("stdout = %q, want resource snippet for %s", stdout, resourcePath)
		}
	}
	if !strings.Contains(stdout, `"mcpServers":`) || !strings.Contains(stdout, `"fetch"`) {
		t.Fatalf("stdout = %q, want mcpServers snippet", stdout)
	}
	if !strings.Contains(stdout, filepath.ToSlash(filepath.Join(powerDir, "hooks", "agent-spawn.kiro.hook"))) {
		t.Fatalf("stdout = %q, want hook file hint", stdout)
	}
	if !strings.Contains(stdout, fmt.Sprintf("Remove: rm -rf %s", powerDir)) {
		t.Fatalf("stdout = %q, want remove hint", stdout)
	}
}

func TestRunPowerInstallRefOverridesGitSource(t *testing.T) {
	orig := powerInstallRun
	t.Cleanup(func() { powerInstallRun = orig })

	var got power.InstallOptions
	powerInstallRun = func(_ context.Context, opts power.InstallOptions) (*power.Result, error) {
		got = opts
		return &power.Result{Name: "repo", PowerDir: "/tmp/repo", ResourcePaths: []string{"/tmp/repo/POWER.md"}}, nil
	}

	if err := run([]string{"power", "install", "https://github.com/o/r", "--ref", "v1.2.3"}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got.Source.Ref != "v1.2.3" {
		t.Fatalf("Source.Ref = %q, want v1.2.3", got.Source.Ref)
	}
}

func TestRunPowerInstallParsesGitHubShorthand(t *testing.T) {
	orig := powerInstallRun
	t.Cleanup(func() { powerInstallRun = orig })

	var got power.InstallOptions
	powerInstallRun = func(_ context.Context, opts power.InstallOptions) (*power.Result, error) {
		got = opts
		return &power.Result{Name: "context7-power", PowerDir: "/tmp/context7-power", ResourcePaths: []string{"/tmp/context7-power/POWER.md"}}, nil
	}

	if err := run([]string{"power", "install", "upstash/context7/tree/master/plugins/context7-power"}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got.Source.Kind != power.SourceGitHubSubdir {
		t.Fatalf("Source.Kind = %q, want %q", got.Source.Kind, power.SourceGitHubSubdir)
	}
	if got.Source.URL != "https://github.com/upstash/context7" {
		t.Fatalf("Source.URL = %q", got.Source.URL)
	}
	if got.Source.Ref != "master" || got.Source.PathInRepo != "plugins/context7-power" {
		t.Fatalf("Source = %#v", got.Source)
	}
}

func TestRunInitHookNoAgents(t *testing.T) {
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	stdout, stderr, err := captureOutput(t, func() error {
		return run([]string{"init-hook"})
	})
	if err != nil {
		t.Fatalf("run(init-hook) error = %v, want nil", err)
	}
	if !strings.Contains(stdout, "No agents found") {
		t.Fatalf("stdout = %q, want 'No agents found'", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestRunInitHookAgentsDirIsFile(t *testing.T) {
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	// Create .kiro/agents as a file to force ReadDir to fail
	if err := os.MkdirAll(".kiro", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(".kiro/agents", []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, _, err = captureOutput(t, func() error {
		return run([]string{"init-hook"})
	})
	if err == nil {
		t.Fatal("run(init-hook) error = nil, want non-nil error")
	}
	if !strings.Contains(err.Error(), "read agents dir") {
		t.Fatalf("error = %v, want 'read agents dir'", err)
	}
}

func TestRunInitHookGlobalUsesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
		t.Setenv("HOMEDRIVE", "")
		t.Setenv("HOMEPATH", "")
	}

	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	if err := os.MkdirAll(".kiro", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(".kiro/agents", []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return run([]string{"init-hook", "--global"})
	})
	if err != nil {
		t.Fatalf("run(init-hook --global) error = %v, want nil", err)
	}
	if !strings.Contains(stdout, "No agents found") {
		t.Fatalf("stdout = %q, want 'No agents found'", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestRunInitHookGlobalTargetDirExclusive(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return run([]string{"init-hook", "--global", "--target-dir", "/tmp/foo"})
	})
	if err == nil {
		t.Fatal("expected error for --global + --target-dir, got nil")
	}
	if !strings.Contains(err.Error(), "--global") || !strings.Contains(err.Error(), "--target-dir") {
		t.Fatalf("error = %q, want message mentioning --global and --target-dir", err.Error())
	}
}

func TestRunInitHookUnknownFlag(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runInitHook([]string{"--unknown"})
	})
	if err == nil {
		t.Fatal("runInitHook(--unknown) error = nil, want non-nil error")
	}
}

func TestRunInitIDEHookInstallAndRemove(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, err := captureOutput(t, func() error {
		return run([]string{"init-ide-hook", "--target-dir", dir})
	})
	if err != nil {
		t.Fatalf("run(init-ide-hook) error = %v", err)
	}
	if !strings.Contains(stdout, "kapm-manual-hook-event.kiro.hook") {
		t.Fatalf("stdout = %q, want installed hook path", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	hookPath := filepath.Join(dir, ".kiro", "hooks", "kapm-manual-hook-event.kiro.hook")
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", hookPath, err)
	}
	if !bytes.Contains(data, []byte(`"type": "userTriggered"`)) {
		t.Fatalf("hook file = %s, want userTriggered trigger", data)
	}
	if !bytes.Contains(data, []byte(`ide-hook-handler --agent 'ide'`)) {
		t.Fatalf("hook file = %s, want ide-hook-handler command", data)
	}
	if !bytes.Contains(data, []byte(`--event 'manual'`)) {
		t.Fatalf("hook file = %s, want event flag", data)
	}

	stdout, stderr, err = captureOutput(t, func() error {
		return run([]string{"init-ide-hook", "--target-dir", dir, "--remove"})
	})
	if err != nil {
		t.Fatalf("run(init-ide-hook --remove) error = %v", err)
	}
	if !strings.Contains(stdout, "kapm-manual-hook-event.kiro.hook") {
		t.Fatalf("stdout = %q, want removed hook path", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
		t.Fatalf("hook should be removed, stat err = %v", err)
	}
}

func TestRunInitIDEHookUnknownFlag(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runInitIDEHook([]string{"--unknown"})
	})
	if err == nil {
		t.Fatal("runInitIDEHook(--unknown) error = nil, want non-nil error")
	}
}

func TestRunHookHandlerWritesJSONLAndUsesEnvFallback(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("AGENT", "env-agent")

	origStdin := os.Stdin
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(stdin): %v", err)
	}
	if _, err := io.WriteString(stdinWriter, `{"hook_event_name":"preToolUse","session_id":"hook-handler-env","cwd":"/w","tool_name":"bash"}`); err != nil {
		t.Fatalf("WriteString(stdin): %v", err)
	}
	if err := stdinWriter.Close(); err != nil {
		t.Fatalf("stdinWriter.Close(): %v", err)
	}
	os.Stdin = stdinReader
	t.Cleanup(func() { os.Stdin = origStdin })
	t.Cleanup(func() { _ = stdinReader.Close() })

	stdout, stderr, err := captureOutput(t, func() error {
		return run([]string{"hook-handler"})
	})
	if err != nil {
		t.Fatalf("run(hook-handler) error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".kapm", "logs", "cli", "hook-handler-env.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(log): %v", err)
	}
	var rec struct {
		Agent string `json:"agent"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &rec); err != nil {
		t.Fatalf("json.Unmarshal(log): %v", err)
	}
	if rec.Agent != "env-agent" {
		t.Fatalf("agent = %q, want env-agent", rec.Agent)
	}
}

func TestRunHookHandlerFlagOverridesEnv(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("AGENT", "env-agent")

	origStdin := os.Stdin
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(stdin): %v", err)
	}
	if _, err := io.WriteString(stdinWriter, `{"hook_event_name":"preToolUse","session_id":"hook-handler-flag","cwd":"/w","tool_name":"bash"}`); err != nil {
		t.Fatalf("WriteString(stdin): %v", err)
	}
	if err := stdinWriter.Close(); err != nil {
		t.Fatalf("stdinWriter.Close(): %v", err)
	}
	os.Stdin = stdinReader
	t.Cleanup(func() { os.Stdin = origStdin })
	t.Cleanup(func() { _ = stdinReader.Close() })

	_, _, err = captureOutput(t, func() error {
		return run([]string{"hook-handler", "--agent", "flag-agent"})
	})
	if err != nil {
		t.Fatalf("run(hook-handler --agent) error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".kapm", "logs", "cli", "hook-handler-flag.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(log): %v", err)
	}
	var rec struct {
		Agent string `json:"agent"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &rec); err != nil {
		t.Fatalf("json.Unmarshal(log): %v", err)
	}
	if rec.Agent != "flag-agent" {
		t.Fatalf("agent = %q, want flag-agent", rec.Agent)
	}
}

func TestRunHookHandlerFallbackEventWhenStdinEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	origStdin := os.Stdin
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(stdin): %v", err)
	}
	if err := stdinWriter.Close(); err != nil {
		t.Fatalf("stdinWriter.Close(): %v", err)
	}
	os.Stdin = stdinReader
	t.Cleanup(func() { os.Stdin = origStdin })
	t.Cleanup(func() { _ = stdinReader.Close() })

	_, stderr, err := captureOutput(t, func() error {
		return run([]string{"hook-handler", "--agent", "ide", "--event", "preToolUse", "--session", "ide"})
	})
	if err != nil {
		t.Fatalf("run(hook-handler fallback) error = %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".kapm", "logs", "cli", "ide.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(fallback log): %v", err)
	}
	var rec struct {
		Agent   string `json:"agent"`
		Session string `json:"session"`
		Event   string `json:"event"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &rec); err != nil {
		t.Fatalf("Unmarshal(log): %v", err)
	}
	if rec.Agent != "ide" || rec.Session != "ide" || rec.Event != "preToolUse" {
		t.Fatalf("record = %#v, want ide/preToolUse fallback", rec)
	}
}

func TestRunIDEHookHandlerWritesMinimalLog(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	stdout, stderr, err := captureOutput(t, func() error {
		return run([]string{"ide-hook-handler", "--agent", "ide", "--event", "preToolUse"})
	})
	if err != nil {
		t.Fatalf("run(ide-hook-handler) error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	logPath := filepath.Join(dir, ".kapm", "logs", "ide", "events.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(ide hook log): %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &rec); err != nil {
		t.Fatalf("Unmarshal(ide hook log): %v", err)
	}
	if rec["event"] != "preToolUse" || rec["agent"] != "ide" || rec["cwd"] == "" {
		t.Fatalf("record = %#v", rec)
	}
	for _, forbidden := range []string{"stdin", "env", "env_keys", "prompt", "session", "tool"} {
		if _, ok := rec[forbidden]; ok {
			t.Fatalf("field %q must not be logged: %#v", forbidden, rec)
		}
	}
}

func captureOutput(t *testing.T, run func() error) (stdout string, stderr string, runErr error) {
	t.Helper()

	origStdout := os.Stdout
	origStderr := os.Stderr

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(stdout): %v", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		if closeErr := stdoutReader.Close(); closeErr != nil {
			t.Fatalf("stdoutReader.Close(): %v", closeErr)
		}
		if closeErr := stdoutWriter.Close(); closeErr != nil {
			t.Fatalf("stdoutWriter.Close(): %v", closeErr)
		}
		t.Fatalf("os.Pipe(stderr): %v", err)
	}

	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter
	defer func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
	}()

	runErr = run()

	if err := stdoutWriter.Close(); err != nil {
		t.Fatalf("stdoutWriter.Close(): %v", err)
	}
	if err := stderrWriter.Close(); err != nil {
		t.Fatalf("stderrWriter.Close(): %v", err)
	}

	stdoutData, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatalf("ReadAll(stdout): %v", err)
	}
	stderrData, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatalf("ReadAll(stderr): %v", err)
	}
	if err := stdoutReader.Close(); err != nil {
		t.Fatalf("stdoutReader.Close(): %v", err)
	}
	if err := stderrReader.Close(); err != nil {
		t.Fatalf("stderrReader.Close(): %v", err)
	}

	return string(stdoutData), string(stderrData), runErr
}

func TestExpandTarget(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	tests := []struct {
		input string
		want  string
	}{
		{"", "."},
		{"~", home},
		{"~/foo", filepath.Join(home, "foo")},
		{"/abs", "/abs"},
		{"./rel", "./rel"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := expandTarget(tt.input)
			if err != nil {
				t.Fatalf("expandTarget(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("expandTarget(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLogsDirResolution(t *testing.T) {
	tests := []struct {
		name        string
		serveArgs   []string
		wantLogsDir string
	}{
		{
			name:        "no args defaults to ./kiro/logs",
			serveArgs:   []string{},
			wantLogsDir: filepath.Join(".", ".kiro", "logs"),
		},
		{
			name:        "target-dir sets logs dir",
			serveArgs:   []string{"--target-dir", "/tmp/x"},
			wantLogsDir: filepath.Join("/tmp/x", ".kiro", "logs"),
		},
		{
			name:        "explicit logs-dir overrides target-dir",
			serveArgs:   []string{"--target-dir", "/tmp/x", "--logs-dir", "/var/log/kapm"},
			wantLogsDir: "/var/log/kapm",
		},
		{
			name:        "explicit logs-dir without target-dir",
			serveArgs:   []string{"--logs-dir", "/var/log/kapm"},
			wantLogsDir: "/var/log/kapm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse flags the same way runServe does, without starting a server.
			fs := flag.NewFlagSet("serve", flag.ContinueOnError)
			logsDir := fs.String("logs-dir", "", "")
			targetDirFlag := fs.String("target-dir", ".", "")
			_ = fs.String("since", "24h", "")
			_ = fs.Int("port", 9090, "")
			_ = fs.Bool("open", false, "")
			if err := fs.Parse(tt.serveArgs); err != nil {
				t.Fatalf("fs.Parse: %v", err)
			}
			targetDir, err := expandTarget(*targetDirFlag)
			if err != nil {
				t.Fatalf("expandTarget: %v", err)
			}
			resolved := *logsDir
			if resolved == "" {
				resolved = filepath.Join(targetDir, ".kiro", "logs")
			}
			if resolved != tt.wantLogsDir {
				t.Fatalf("logsDir = %q, want %q", resolved, tt.wantLogsDir)
			}
		})
	}
}

func TestInstallGlobalSetsRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
		t.Setenv("HOMEDRIVE", "")
		t.Setenv("HOMEPATH", "")
	}

	origInstallRun := installRun
	t.Cleanup(func() { installRun = origInstallRun })

	var got install.Options
	installRun = func(opts install.Options) error {
		got = opts
		return nil
	}

	if err := run([]string{"install", "--global", "owner/repo"}); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	if got.Root != home {
		t.Fatalf("Root = %q, want %q", got.Root, home)
	}
}

func TestGlobalTargetDirExclusive(t *testing.T) {
	origInstallRun := installRun
	t.Cleanup(func() { installRun = origInstallRun })
	installRun = func(opts install.Options) error { return nil }

	_, _, err := captureOutput(t, func() error {
		return run([]string{"install", "--global", "--target-dir", "/tmp/foo", "owner/repo"})
	})
	if err == nil {
		t.Fatal("expected error for --global + --target-dir, got nil")
	}
	if !strings.Contains(err.Error(), "--global") || !strings.Contains(err.Error(), "--target-dir") {
		t.Fatalf("error = %q, want message mentioning --global and --target-dir", err.Error())
	}
}
