//go:build e2e

// Package e2e exercises the kapm and kapl binaries as end-to-end
// subprocesses. These tests build the binaries once per package and run them
// against temporary directories to catch breakage at the CLI contract layer
// (argument parsing, exit codes, stdin/stdout, on-disk artifacts) that unit
// tests cannot cover.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var (
	buildOnce sync.Once
	kapmBin   string
	loggerBin string
	buildErr  error
)

// binaries builds kapm and kapl once per test process and returns their
// absolute paths. Subsequent calls reuse the cached result.
func binaries(t *testing.T) (kapm, logger string) {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "kapm-e2e-bin-*")
		if err != nil {
			buildErr = err
			return
		}
		kapmBin = filepath.Join(dir, "kapm")
		loggerBin = filepath.Join(dir, "kapl")

		repoRoot, err := findRepoRoot()
		if err != nil {
			buildErr = err
			return
		}
		for _, b := range []struct{ out, pkg string }{
			{kapmBin, "./cmd/kapm"},
			{loggerBin, "./cmd/kapl"},
		} {
			cmd := exec.Command("go", "build", "-o", b.out, b.pkg)
			cmd.Dir = repoRoot
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				buildErr = err
				return
			}
		}
	})
	if buildErr != nil {
		t.Fatalf("build binaries: %v", buildErr)
	}
	return kapmBin, loggerBin
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
