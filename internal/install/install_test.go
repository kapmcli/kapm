package install_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kapmcli/kapm/internal/install"
	"github.com/kapmcli/kapm/internal/testutil"
)

func TestInstallPrefersApmWhenAvailable(t *testing.T) {
	root := installFixtureRoot(t, "manifest-project")
	binDir := t.TempDir()
	argsFile := mockCommand(t, binDir, "apm", 0)
	mockCommand(t, binDir, "uvx", 0)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := install.Run(install.Options{Root: root, InstallArgs: []string{"github/awesome-copilot/skills/review-and-refactor"}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	assertCommandArgs(t, argsFile, "install github/awesome-copilot/skills/review-and-refactor")
	testutil.AssertDirEqual(t, filepath.Join(root, ".kiro"), filepath.Join(repoTestdataRoot(), "install", "manifest-project", "expected", ".kiro"))
}

func TestInstallPassesUpdateFlag(t *testing.T) {
	root := installFixtureRoot(t, "manifest-project")
	binDir := t.TempDir()
	argsFile := mockCommand(t, binDir, "apm", 0)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := install.Run(install.Options{Root: root, InstallArgs: []string{"--update", "microsoft/apm-sample-package"}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	assertCommandArgs(t, argsFile, "install --update microsoft/apm-sample-package")
}

func TestInstallPreservesArbitraryApmArgs(t *testing.T) {
	root := installFixtureRoot(t, "manifest-project")
	binDir := t.TempDir()
	argsFile := mockCommand(t, binDir, "apm", 0)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := install.Run(install.Options{Root: root, InstallArgs: []string{"--dry-run", "--update", "github/awesome-copilot/skills/review-and-refactor"}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	assertCommandArgs(t, argsFile, "install --dry-run --update github/awesome-copilot/skills/review-and-refactor")
}

func TestInstallFallsBackToUvx(t *testing.T) {
	root := installFixtureRoot(t, "manifest-project")
	binDir := t.TempDir()
	argsFile := mockCommand(t, binDir, "uvx", 0)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := install.Run(install.Options{Root: root}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	assertCommandArgs(t, argsFile, "--from apm-cli==0.9.1 apm install")
	testutil.AssertDirEqual(t, filepath.Join(root, ".kiro"), filepath.Join(repoTestdataRoot(), "install", "manifest-project", "expected", ".kiro"))
}

func TestInstallCommandNotFound(t *testing.T) {
	root := installFixtureRoot(t, "manifest-project")
	t.Setenv("PATH", t.TempDir())

	err := install.Run(install.Options{Root: root})
	if err == nil || !strings.Contains(err.Error(), "neither `apm` nor `uvx` was found") {
		t.Fatalf("Run() error = %v, want missing installer error", err)
	}
}

func TestInstallCommand_WrapsError(t *testing.T) {
	root := installFixtureRoot(t, "manifest-project")
	t.Setenv("PATH", t.TempDir())

	err := install.Run(install.Options{Root: root})
	if err == nil || !strings.Contains(err.Error(), "install command") {
		t.Fatalf("Run() error = %v, want error containing %q", err, "install command")
	}
}

func TestInstallCommandFails(t *testing.T) {
	root := installFixtureRoot(t, "manifest-project")
	binDir := t.TempDir()
	mockCommand(t, binDir, "apm", 23)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := install.Run(install.Options{Root: root})
	if err == nil || !strings.Contains(err.Error(), "apm install") {
		t.Fatalf("Run() error = %v, want apm install failure", err)
	}
}

func installFixtureRoot(t *testing.T, fixture string) string {
	t.Helper()

	root := t.TempDir()
	testutil.CopyDir(t, filepath.Join(repoTestdataRoot(), "install", fixture, "input"), root)
	return root
}

func mockCommand(t *testing.T, binDir, name string, exitCode int) string {
	t.Helper()

	argsFile := filepath.Join(binDir, name+".args")
	script := filepath.Join(binDir, name)
	content := "#!/bin/sh\n" +
		"printf '%s' \"$*\" > " + strconv.Quote(argsFile) + "\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", script, err)
	}
	return argsFile
}

func assertCommandArgs(t *testing.T, argsFile, want string) {
	t.Helper()

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", argsFile, err)
	}
	if got := strings.TrimSpace(string(data)); got != want {
		t.Fatalf("command args = %q, want %q", got, want)
	}
}

func repoTestdataRoot() string {
	return filepath.Join("..", "..", "testdata")
}
