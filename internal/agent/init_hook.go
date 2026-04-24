package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"github.com/kapmcli/kapm/internal/cli"
	"github.com/kapmcli/kapm/internal/paths"
)

// InitHookOptions configures an init-hook run.
type InitHookOptions struct {
	Root   string
	Remove bool
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
}

// InitHook interactively adds or removes kapm hook entries in .kiro/agents/*.json.
func InitHook(opts InitHookOptions) error {
	applyDefaults(&opts.Root, &opts.In, &opts.Out)
	if opts.Err == nil {
		opts.Err = os.Stderr
	}
	return initHook(opts)
}

func initHook(opts InitHookOptions) error {
	agentsDir := filepath.Join(opts.Root, paths.KiroDir, paths.AgentsSubdir)
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			_, _ = fmt.Fprintln(opts.Out, "No agents found. Create agents with `kapm agent generate` first.")
			return nil
		}
		return fmt.Errorf("read agents dir: %w", err)
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	slices.Sort(names)

	if len(names) == 0 {
		_, _ = fmt.Fprintln(opts.Out, "No agents found. Create agents with `kapm agent generate` first.")
		return nil
	}

	p := cli.NewPrompter(opts.In, opts.Out)
	selected, err := p.MultiSelect("Select agents", names, true)
	if err != nil {
		return fmt.Errorf("agent selection: %w", err)
	}
	if len(selected) == 0 {
		return nil
	}

	if !opts.Remove {
		hooksDir := HooksDir(opts.Root)
		if err := os.MkdirAll(hooksDir, 0o755); err != nil {
			return fmt.Errorf("mkdir hooks: %w", err)
		}
		loggerPath := LoggerBinaryPath(opts.Root)
		if err := os.WriteFile(loggerPath, kaplBinary, 0o755); err != nil {
			return fmt.Errorf("write kapl: %w", err)
		}
	}

	var failed []string
	for _, name := range selected {
		agentPath := AgentFile(opts.Root, name)
		if err := processAgent(agentPath, name, opts.Remove); err != nil {
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

func processAgent(agentPath, name string, remove bool) error {
	rawMap, _, err := readAgentRawJSON(agentPath)
	if err != nil {
		return err
	}

	hooksMap := make(map[string][]json.RawMessage)
	if raw, ok := rawMap["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooksMap); err != nil {
			return err
		}
	}

	if remove {
		removeKapmEntries(hooksMap)
	} else {
		command := fmt.Sprintf("AGENT=%s .kiro/hooks/kapl", name)
		if err := addKapmEntries(hooksMap, command); err != nil {
			return err
		}
	}

	// Clean up empty event arrays
	for k, v := range hooksMap {
		if len(v) == 0 {
			delete(hooksMap, k)
		}
	}

	if len(hooksMap) == 0 {
		delete(rawMap, "hooks")
	} else {
		hooksRaw, err := json.Marshal(hooksMap)
		if err != nil {
			return fmt.Errorf("marshal hooks: %w", err)
		}
		rawMap["hooks"] = json.RawMessage(hooksRaw)
	}

	return writeAgentRawJSON(agentPath, rawMap)
}

// isKapmEntry reports whether raw is a kapm-managed hook entry.
// If raw is malformed JSON, corrupt is true and match is false; callers must
// not overwrite the entry.
func isKapmEntry(raw json.RawMessage) (match, corrupt bool) {
	var entry struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return false, true
	}
	match = strings.HasSuffix(entry.Command, " hook-handler") ||
		strings.HasSuffix(entry.Command, "/kapl")
	return match, false
}

func removeKapmEntries(hooksMap map[string][]json.RawMessage) {
	for event, entries := range hooksMap {
		hooksMap[event] = filterHooks(entries, func(e json.RawMessage) bool {
			match, corrupt := isKapmEntry(e)
			if corrupt {
				slog.Warn("corrupt hook entry preserved", "event", event)
				return true
			}
			return !match
		})
	}
}

func addKapmEntries(hooksMap map[string][]json.RawMessage, command string) error {
	// Fail-fast: refuse to overwrite if any existing entry is corrupt.
	for event, entries := range hooksMap {
		for _, e := range entries {
			if _, corrupt := isKapmEntry(e); corrupt {
				slog.Warn("corrupt hook entry; aborting update", "event", event)
				return fmt.Errorf("corrupt hook entry in event %q", event)
			}
		}
	}
	for _, event := range apmconfig.HookEvents {
		var entry map[string]string
		if event == apmconfig.EventPreToolUse || event == apmconfig.EventPostToolUse {
			entry = map[string]string{"matcher": "*", "command": command}
		} else {
			entry = map[string]string{"command": command}
		}
		entryRaw, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("marshal hook entry for %q: %w", event, err)
		}

		// Remove existing kapm entries for this event, then append new one
		hooksMap[event] = append(filterHooks(hooksMap[event], func(e json.RawMessage) bool {
			match, _ := isKapmEntry(e) // corrupt already caught above
			return !match
		}), json.RawMessage(entryRaw))
	}
	return nil
}
