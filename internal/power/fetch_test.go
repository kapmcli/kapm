package power

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLocalFetcher(t *testing.T) {
	f := localFetcher{}
	src := PowerSource{Kind: SourceLocal, Path: "/some/local/path"}

	dir, commit, cleanup, err := f.Fetch(context.Background(), src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != src.Path {
		t.Errorf("dir = %q, want %q", dir, src.Path)
	}
	if commit != "" {
		t.Errorf("commit = %q, want empty", commit)
	}
	if cleanup == nil {
		t.Error("cleanup must not be nil")
	}
	cleanup()
}

func TestNewGitCommandEnv(t *testing.T) {
	cmd := newGitCommand(context.Background(), "", "version")
	found := false
	for _, e := range cmd.Env {
		if e == "GIT_TERMINAL_PROMPT=0" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("GIT_TERMINAL_PROMPT=0 not found in env: %v", cmd.Env)
	}
}

func TestNewGitCommandDir(t *testing.T) {
	cmd := newGitCommand(context.Background(), "/tmp", "version")
	if cmd.Dir != "/tmp" {
		t.Errorf("cmd.Dir = %q, want %q", cmd.Dir, "/tmp")
	}
	cmd2 := newGitCommand(context.Background(), "", "version")
	if cmd2.Dir != "" {
		t.Errorf("cmd.Dir = %q, want empty when dir is empty", cmd2.Dir)
	}
}

func TestGitFetcher_SparseCheckoutArgv(t *testing.T) {
	binDir, logPath := writeFakeGitScript(t, fakeGitOptions{
		subpath: "sub/dir",
		commit:  "abc123",
	})
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	f := gitFetcher{}
	src := PowerSource{
		Kind:       SourceGitHubSubdir,
		URL:        "https://github.com/o/r",
		Owner:      "o",
		Repo:       "r",
		Ref:        "main",
		PathInRepo: "sub/dir",
	}

	localDir, commit, cleanup, err := f.Fetch(context.Background(), src)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if commit != "abc123" {
		t.Fatalf("commit = %q, want abc123", commit)
	}
	if got := filepath.ToSlash(localDir); !strings.HasSuffix(got, "/sparse/sub/dir") {
		t.Fatalf("localDir = %q, want sparse subpath", got)
	}
	defer cleanup()

	got := readGitLog(t, logPath)
	want := []string{
		"0|init",
		"0|remote add origin https://github.com/o/r.git",
		"0|sparse-checkout init --cone",
		"0|sparse-checkout set sub/dir",
		"0|fetch --depth=1 origin main",
		"0|checkout FETCH_HEAD",
		"0|rev-parse HEAD",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("git argv mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestGitFetcher_FullCloneArgv(t *testing.T) {
	binDir, logPath := writeFakeGitScript(t, fakeGitOptions{
		commit: "abc123",
	})
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	f := gitFetcher{}
	src := PowerSource{
		Kind: SourceGitRoot,
		URL:  "https://github.com/o/r",
		Ref:  "main",
	}

	localDir, commit, cleanup, err := f.Fetch(context.Background(), src)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if commit != "abc123" {
		t.Fatalf("commit = %q, want abc123", commit)
	}
	if got := filepath.ToSlash(localDir); !strings.HasSuffix(got, "/repo") {
		t.Fatalf("localDir = %q, want repo root", got)
	}
	defer cleanup()

	got := readGitLog(t, logPath)
	wantPrefix := []string{
		"0|clone --depth=1 --branch main https://github.com/o/r",
		"0|rev-parse HEAD",
	}
	if len(got) < len(wantPrefix) {
		t.Fatalf("git argv too short: %v", got)
	}
	for i, want := range wantPrefix {
		if !strings.HasPrefix(got[i], want) {
			t.Fatalf("git argv[%d] = %q, want prefix %q", i, got[i], want)
		}
	}
}

func TestGitFetcher_CleanupRemovesTempDir(t *testing.T) {
	binDir, _ := writeFakeGitScript(t, fakeGitOptions{commit: "abc123"})
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	f := gitFetcher{}
	localDir, _, cleanup, err := f.Fetch(context.Background(), PowerSource{
		Kind: SourceGitRoot,
		URL:  "https://example.com/repo.git",
		Ref:  "main",
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	root := filepath.Dir(localDir)
	cleanup()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("cleanup did not remove %q, stat err = %v", root, err)
	}
}

type fakeGitOptions struct {
	subpath string
	commit  string
	failOn  string
}

func writeFakeGitScript(t *testing.T, opts fakeGitOptions) (string, string) {
	t.Helper()

	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "git.log")
	if runtime.GOOS == "windows" {
		scriptPath := filepath.Join(binDir, "git.bat")
		script := "@echo off\r\n" +
			"setlocal EnableDelayedExpansion\r\n" +
			">> \"" + logPath + "\" echo %GIT_TERMINAL_PROMPT%^|%*\r\n" +
			"if not \"%FAIL_ON%\"==\"\" if \"%*\"==\"%FAIL_ON%\" exit /b 17\r\n" +
			"if \"%1\"==\"init\" (\r\n" +
			"  mkdir .git 2>nul\r\n" +
			"  exit /b 0\r\n" +
			")\r\n" +
			"if \"%1\"==\"clone\" (\r\n" +
			"  set \"dest=\"\r\n" +
			"  for %%A in (%*) do set \"dest=%%~A\"\r\n" +
			"  mkdir \"!dest!\\.git\" 2>nul\r\n" +
			"  if not \"%SUBPATH%\"==\"\" mkdir \"!dest!\\%SUBPATH%\" 2>nul\r\n" +
			"  exit /b 0\r\n" +
			")\r\n" +
			"if \"%1\"==\"checkout\" (\r\n" +
			"  if not \"%SUBPATH%\"==\"\" mkdir \"%CD%\\%SUBPATH%\" 2>nul\r\n" +
			"  exit /b 0\r\n" +
			")\r\n" +
			"if \"%1\"==\"rev-parse\" (\r\n" +
			"  echo %COMMIT%\r\n" +
			"  exit /b 0\r\n" +
			")\r\n" +
			"exit /b 0\r\n"
		if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
			t.Fatalf("WriteFile(%q): %v", scriptPath, err)
		}
	} else {
		scriptPath := filepath.Join(binDir, "git")
		script := "#!/usr/bin/env bash\n" +
			"set -euo pipefail\n" +
			"printf '%s|%s\\n' \"${GIT_TERMINAL_PROMPT:-}\" \"$*\" >> " + shellQuote(logPath) + "\n" +
			"if [[ -n ${FAIL_ON:-} && \"$*\" == \"$FAIL_ON\" ]]; then exit 17; fi\n" +
			"case \"$1\" in\n" +
			"  init)\n" +
			"    mkdir -p .git\n" +
			"    ;;\n" +
			"  clone)\n" +
			"    dest=\"${@: -1}\"\n" +
			"    mkdir -p \"$dest/.git\"\n" +
			"    if [[ -n ${SUBPATH:-} ]]; then mkdir -p \"$dest/$SUBPATH\"; fi\n" +
			"    ;;\n" +
			"  checkout)\n" +
			"    if [[ -n ${SUBPATH:-} ]]; then mkdir -p \"$PWD/$SUBPATH\"; fi\n" +
			"    ;;\n" +
			"  rev-parse)\n" +
			"    printf '%s\\n' \"${COMMIT:-deadbeef}\"\n" +
			"    ;;\n" +
			"esac\n"
		if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
			t.Fatalf("WriteFile(%q): %v", scriptPath, err)
		}
	}

	t.Setenv("FAIL_ON", opts.failOn)
	t.Setenv("SUBPATH", opts.subpath)
	t.Setenv("COMMIT", opts.commit)
	return binDir, logPath
}

func readGitLog(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	rawLines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(rawLines) == 1 && rawLines[0] == "" {
		return nil
	}
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		lines = append(lines, strings.TrimRight(line, "\r"))
	}
	return lines
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func TestValidateGitRef(t *testing.T) {
	valid := []string{"main", "feature/branch-1", "v1.2.3", "release/v1.0.0_rc1", "abc1234", "abc1234567890abcdef1234567890abcdef123456"}
	for _, ref := range valid {
		if err := validateGitRef(ref); err != nil {
			t.Errorf("validateGitRef(%q) = %v, want nil", ref, err)
		}
	}

	type badCase struct {
		ref     string
		wantMsg string
	}
	bad := []badCase{
		{"--upload-pack=evil", "starts with '-'"},
		{"-fuzz=1", "starts with '-'"},
		{"", "empty"},
		{"abc;rm -rf /", "invalid characters"},
		{"foo bar", "invalid characters"},
	}
	for _, tc := range bad {
		err := validateGitRef(tc.ref)
		if err == nil {
			t.Errorf("validateGitRef(%q) = nil, want error containing %q", tc.ref, tc.wantMsg)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantMsg) {
			t.Errorf("validateGitRef(%q) error = %q, want it to contain %q", tc.ref, err.Error(), tc.wantMsg)
		}
	}
}

func TestGitFetcher_InvalidRefNotInvokeGit(t *testing.T) {
	binDir, logPath := writeFakeGitScript(t, fakeGitOptions{commit: "abc123"})
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	f := gitFetcher{}
	_, _, _, err := f.Fetch(context.Background(), PowerSource{
		Kind: SourceGitRoot,
		URL:  "https://github.com/o/r",
		Ref:  "--evil",
	})
	if err == nil {
		t.Fatal("expected error for invalid ref, got nil")
	}

	// fake git log must not exist (git was never invoked)
	if _, statErr := os.Stat(logPath); !os.IsNotExist(statErr) {
		t.Errorf("fake git was invoked for invalid ref; log exists at %q", logPath)
	}
}

// TestGitFetcher_SubpathNotFoundIncludesURL verifies that the subpath-not-found error
// includes both the subpath and the repository URL.
func TestGitFetcher_SubpathNotFoundIncludesURL(t *testing.T) {
	// Use a fake git that does NOT create the subpath directory.
	binDir, _ := writeFakeGitScript(t, fakeGitOptions{
		commit:  "abc123",
		subpath: "", // no subpath created by fake git
	})
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	f := gitFetcher{}
	src := PowerSource{
		Kind:       SourceGitRoot,
		URL:        "https://github.com/example/repo",
		PathInRepo: "missing/subdir",
	}

	_, _, _, err := f.Fetch(context.Background(), src)
	if err == nil {
		t.Fatal("expected error for missing subpath, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "missing/subdir") {
		t.Errorf("error should contain subpath, got: %q", msg)
	}
	if !strings.Contains(msg, "https://github.com/example/repo") {
		t.Errorf("error should contain repo URL, got: %q", msg)
	}
}
