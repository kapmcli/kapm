//go:build e2e

// Package e2e exercises the kapm binary as end-to-end
// subprocesses. These tests build the binary once per package and run it
// against temporary directories to catch breakage at the CLI contract layer
// (argument parsing, exit codes, stdin/stdout, on-disk artifacts) that unit
// tests cannot cover.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var (
	buildOnce sync.Once
	kapmBin   string
	buildErr  error
)

// binary builds kapm once per test process and returns its absolute path.
// Subsequent calls reuse the cached result.
func binary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "kapm-e2e-bin-*")
		if err != nil {
			buildErr = err
			return
		}
		kapmBin = filepath.Join(dir, executableName("kapm"))

		repoRoot, err := findRepoRoot()
		if err != nil {
			buildErr = err
			return
		}
		cmd := exec.Command("go", "build", "-o", kapmBin, "./cmd/kapm")
		cmd.Dir = repoRoot
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			buildErr = err
			return
		}
	})
	if buildErr != nil {
		t.Fatalf("build binaries: %v", buildErr)
	}
	return kapmBin
}

func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// findRepoRoot walks up from CWD looking for go.mod.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
