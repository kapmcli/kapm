package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/kapmcli/kapm/internal/install"
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

func TestRunInitHookUnknownFlag(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runInitHook([]string{"--unknown"})
	})
	if err == nil {
		t.Fatal("runInitHook(--unknown) error = nil, want non-nil error")
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
