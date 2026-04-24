package install

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/kapmcli/kapm/internal/syncer"
)

// apmCliPin is the pinned apm-cli PyPI version used by the uvx fallback.
// Bump deliberately after verifying compatibility with microsoft/apm changes:
// https://github.com/microsoft/apm/releases
const apmCliPin = "apm-cli==0.9.1"

var lookPath = exec.LookPath
var newCommand = func(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

// Options controls an install run.
type Options struct {
	InstallArgs []string
	Root        string
	Force       bool
}

// Run delegates package installation to apm-cli, then syncs .kiro output.
func Run(opts Options) error {
	root := opts.Root
	if root == "" {
		root = "."
	}

	cmd, err := installCommand(opts.InstallArgs)
	if err != nil {
		return fmt.Errorf("install command: %w", err)
	}
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("apm install: %w", err)
	}

	if err := syncer.Run(syncer.Options{Root: root, Force: opts.Force}); err != nil {
		return fmt.Errorf("sync after install: %w", err)
	}

	return nil
}

func installCommand(installArgs []string) (*exec.Cmd, error) {
	args := append([]string(nil), installArgs...)

	if _, err := lookPath("apm"); err == nil {
		return newCommand("apm", append([]string{"install"}, args...)...), nil
	}

	if _, err := lookPath("uvx"); err == nil {
		return newCommand("uvx", append([]string{"--from", apmCliPin, "apm", "install"}, args...)...), nil
	}

	return nil, errors.New("neither `apm` nor `uvx` was found. Install APM or uv: https://github.com/astral-sh/uv")
}
