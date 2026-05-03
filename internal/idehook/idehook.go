// Package idehook installs Kiro IDE hook files that forward lifecycle events to kapm.
package idehook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"github.com/kapmcli/kapm/internal/fileutil"
	"github.com/kapmcli/kapm/internal/paths"
)

const agentName = "ide"

// Options configures an init-ide-hook run.
type Options struct {
	Root       string
	Remove     bool
	Executable string
	Out        io.Writer
}

type hookSpec struct {
	Event       string
	WhenType    string
	ToolTypes   []string
	Patterns    []string
	FileName    string
	Name        string
	Description string
}

var hookSpecs = []hookSpec{
	{
		Event:       "manual",
		WhenType:    "userTriggered",
		FileName:    "kapm-manual-dump.kiro.hook",
		Name:        "kapm Manual Hook Dump",
		Description: "Manually dump Kiro IDE hook input for kapm debugging.",
	},
	{
		Event:       apmconfig.EventUserPromptSubmit,
		WhenType:    "promptSubmit",
		FileName:    "kapm-prompt-submit.kiro.hook",
		Name:        "kapm Prompt Submit Logger",
		Description: "Record Kiro IDE prompt submit events for kapm monitoring.",
	},
	{
		Event:       apmconfig.EventPreToolUse,
		WhenType:    "preToolUse",
		ToolTypes:   []string{"*"},
		FileName:    "kapm-pre-tool-use.kiro.hook",
		Name:        "kapm Pre Tool Use Logger",
		Description: "Record Kiro IDE tool start events for kapm monitoring.",
	},
	{
		Event:       apmconfig.EventPostToolUse,
		WhenType:    "postToolUse",
		ToolTypes:   []string{"*"},
		FileName:    "kapm-post-tool-use.kiro.hook",
		Name:        "kapm Post Tool Use Logger",
		Description: "Record Kiro IDE tool completion events for kapm monitoring.",
	},
	{
		Event:       apmconfig.EventStop,
		WhenType:    "agentStop",
		FileName:    "kapm-stop.kiro.hook",
		Name:        "kapm Stop Logger",
		Description: "Record Kiro IDE assistant stop events for kapm monitoring.",
	},
	{
		Event:       apmconfig.EventFileCreated,
		WhenType:    "fileCreated",
		Patterns:    []string{"**/*"},
		FileName:    "kapm-file-created.kiro.hook",
		Name:        "kapm File Created Logger",
		Description: "Record Kiro IDE file creation events for kapm monitoring.",
	},
	{
		Event:       apmconfig.EventFileEdited,
		WhenType:    "fileEdited",
		Patterns:    []string{"**/*"},
		FileName:    "kapm-file-edited.kiro.hook",
		Name:        "kapm File Edited Logger",
		Description: "Record Kiro IDE file edit events for kapm monitoring.",
	},
	{
		Event:       apmconfig.EventFileDeleted,
		WhenType:    "fileDeleted",
		Patterns:    []string{"**/*"},
		FileName:    "kapm-file-deleted.kiro.hook",
		Name:        "kapm File Deleted Logger",
		Description: "Record Kiro IDE file deletion events for kapm monitoring.",
	},
	{
		Event:       apmconfig.EventPreTaskExecution,
		WhenType:    "preTaskExecution",
		FileName:    "kapm-pre-task-execution.kiro.hook",
		Name:        "kapm Pre Task Execution Logger",
		Description: "Record Kiro IDE spec task start events for kapm monitoring.",
	},
	{
		Event:       apmconfig.EventPostTaskExecution,
		WhenType:    "postTaskExecution",
		FileName:    "kapm-post-task-execution.kiro.hook",
		Name:        "kapm Post Task Execution Logger",
		Description: "Record Kiro IDE spec task completion events for kapm monitoring.",
	},
}

var obsoleteHookFiles = []string{
	"kapm-file-saved.kiro.hook",
}

type hookFile struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Enabled     bool     `json:"enabled"`
	When        hookWhen `json:"when"`
	Then        hookThen `json:"then"`
}

type hookWhen struct {
	Type      string   `json:"type"`
	ToolTypes []string `json:"toolTypes,omitempty"`
	Patterns  []string `json:"patterns,omitempty"`
}

type hookThen struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// Init installs or removes kapm-managed Kiro IDE hook files under .kiro/hooks.
func Init(opts Options) error {
	if opts.Root == "" {
		opts.Root = "."
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Remove {
		return remove(opts.Root, opts.Out)
	}

	executablePath, err := resolveExecutablePath(opts.Executable)
	if err != nil {
		return fmt.Errorf("resolve hook executable: %w", err)
	}
	return install(opts.Root, executablePath, opts.Out)
}

func install(root, executablePath string, out io.Writer) error {
	if err := removeObsolete(root); err != nil {
		return err
	}
	for _, spec := range hookSpecs {
		path := hookPath(root, spec.FileName)
		data, err := renderHook(spec, hookCommand(executablePath, spec.Event))
		if err != nil {
			return err
		}
		if _, err := fileutil.WriteFileAtomic(path, data, true); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "  ✓ %s\n", path)
	}
	return nil
}

func removeObsolete(root string) error {
	for _, fileName := range obsoleteHookFiles {
		path := hookPath(root, fileName)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove obsolete IDE hook %q: %w", path, err)
		}
	}
	return nil
}

func remove(root string, out io.Writer) error {
	for _, spec := range hookSpecs {
		path := hookPath(root, spec.FileName)
		if err := os.Remove(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("remove %q: %w", path, err)
		}
		_, _ = fmt.Fprintf(out, "  ✓ %s\n", path)
	}
	for _, fileName := range obsoleteHookFiles {
		path := hookPath(root, fileName)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove obsolete IDE hook %q: %w", path, err)
		}
	}
	return removeHooksDirIfEmpty(root)
}

func removeHooksDirIfEmpty(root string) error {
	hooksDir := filepath.Join(root, paths.KiroDir, paths.HooksSubdir)
	entries, err := os.ReadDir(hooksDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read hooks dir %q: %w", hooksDir, err)
	}
	if len(entries) == 0 {
		if err := os.Remove(hooksDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove empty hooks dir %q: %w", hooksDir, err)
		}
	}
	return nil
}

func hookPath(root, fileName string) string {
	return filepath.Join(root, paths.KiroDir, paths.HooksSubdir, fileName)
}

func hookCommand(executablePath, event string) string {
	return fmt.Sprintf("%s hook-dump --agent %s --event %s", strconv.Quote(executablePath), agentName, event)
}

func renderHook(spec hookSpec, command string) ([]byte, error) {
	hook := hookFile{
		Name:        spec.Name,
		Description: spec.Description,
		Version:     "1.0.0",
		Enabled:     true,
		When:        hookWhen{Type: spec.WhenType, ToolTypes: spec.ToolTypes, Patterns: spec.Patterns},
		Then: hookThen{
			Type:    "runCommand",
			Command: command,
		},
	}
	data, err := json.MarshalIndent(hook, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal IDE hook %q: %w", spec.FileName, err)
	}
	return append(data, '\n'), nil
}

func resolveExecutablePath(executable string) (string, error) {
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
