package agent

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kapmcli/kapm/internal/cli"
	"github.com/kapmcli/kapm/internal/paths"
)

// InitHookOptions configures an init-hook run.
type InitHookOptions struct {
	Root       string
	Remove     bool
	Executable string
	In         io.Reader
	Out        io.Writer
	Err        io.Writer
}

// InitHook interactively adds or removes kapm hook entries in .kiro/agents/*.json.
func InitHook(opts InitHookOptions) error {
	applyDefaults(&opts.Root, &opts.In, &opts.Out)
	if opts.Err == nil {
		opts.Err = os.Stderr
	}
	executablePath, err := resolveHookExecutablePath(opts.Executable)
	if err != nil {
		return fmt.Errorf("resolve hook executable: %w", err)
	}
	opts.Executable = executablePath
	return initHook(opts)
}

func resolveHookExecutablePath(executable string) (string, error) {
	if executable == "" {
		invokedPath := os.Args[0]
		if invokedPath == "" {
			detected, err := os.Executable()
			if err != nil {
				return "", fmt.Errorf("determine kapm executable: %w", err)
			}
			executable = detected
		} else if strings.ContainsRune(invokedPath, os.PathSeparator) {
			executable = invokedPath
		} else {
			lookedUp, err := exec.LookPath(invokedPath)
			if err != nil {
				return "", fmt.Errorf("resolve kapm executable %q: %w", invokedPath, err)
			}
			executable = lookedUp
		}
	}
	absPath, err := filepath.Abs(executable)
	if err != nil {
		return "", fmt.Errorf("abs kapm executable %q: %w", executable, err)
	}
	return absPath, nil
}

func initHook(opts InitHookOptions) error {
	names, err := loadAgentNames(opts.Root)
	if err != nil {
		return fmt.Errorf("load agent names: %w", err)
	}
	if len(names) == 0 {
		printNoAgentsFound(opts.Out)
		return nil
	}

	selected, err := selectAgents(opts.In, opts.Out, names)
	if err != nil {
		return fmt.Errorf("agent selection: %w", err)
	}
	if len(selected) == 0 {
		return nil
	}
	if err := cleanupLegacyHookArtifacts(opts.Root); err != nil {
		return fmt.Errorf("cleanup legacy artifacts: %w", err)
	}
	return processSelectedAgents(opts, selected)
}

func loadAgentNames(root string) ([]string, error) {
	agentsDir := filepath.Join(root, paths.KiroDir, paths.AgentsSubdir)
	info, err := os.Stat(agentsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read agents dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("read agents dir: %w", fs.ErrInvalid)
	}

	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return nil, fmt.Errorf("read agents dir: %w", err)
	}
	return collectAgentNames(entries)
}

func collectAgentNames(entries []os.DirEntry) ([]string, error) {
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name, err := validateAndNormalizeName(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			return nil, fmt.Errorf("invalid agent file name %q: %w", e.Name(), err)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func printNoAgentsFound(out io.Writer) {
	_, _ = fmt.Fprintln(out, "No agents found. Create agents with `kapm agent generate` first.")
}

func selectAgents(in io.Reader, out io.Writer, names []string) ([]string, error) {
	p := cli.NewPrompter(in, out)
	return p.MultiSelect("Select agents", names, true)
}

func processSelectedAgents(opts InitHookOptions, selected []string) error {
	var failed []string
	for _, name := range selected {
		agentPath := AgentFile(opts.Root, name)
		if err := processAgent(agentPath, opts.Executable, name, opts.Remove); err != nil {
			_, _ = fmt.Fprintf(opts.Err, "  ✗ %s: %v\n", name, err)
			failed = append(failed, name)
		} else {
			_, _ = fmt.Fprintf(opts.Out, "  ✓ %s\n", name)
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("%d of %d agents failed: %s", len(failed), len(selected), strings.Join(failed, ", "))
	}
	return nil
}

func cleanupLegacyHookArtifacts(root string) error {
	hooksDir := filepath.Join(root, paths.KiroDir, paths.HooksSubdir)
	for _, name := range []string{"kapl", "kapl.exe"} {
		legacyPath := filepath.Join(hooksDir, name)
		if err := os.Remove(legacyPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove legacy hook helper %q: %w", legacyPath, err)
		}
	}
	entries, err := os.ReadDir(hooksDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read hooks dir %q: %w", hooksDir, err)
	}
	if len(entries) == 0 {
		if err := os.Remove(hooksDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove empty hooks dir %q: %w", hooksDir, err)
		}
	}
	return nil
}
