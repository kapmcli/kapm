package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"github.com/kapmcli/kapm/internal/cli"
	"github.com/kapmcli/kapm/internal/paths"
)

// InitHookOptions configures an init-hook run.
type InitHookOptions struct {
	Root   string
	Remove bool
	Executable string
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
	executablePath, err := resolveHookExecutablePath(opts.Executable)
	if err != nil {
		return err
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
	agentsDir := filepath.Join(opts.Root, paths.KiroDir, paths.AgentsSubdir)
	info, err := os.Stat(agentsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			_, _ = fmt.Fprintln(opts.Out, "No agents found. Create agents with `kapm agent generate` first.")
			return nil
		}
		return fmt.Errorf("read agents dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("read agents dir: %w", fs.ErrInvalid)
	}

	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return fmt.Errorf("read agents dir: %w", err)
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			name, err := validateAndNormalizeName(strings.TrimSuffix(e.Name(), ".json"))
			if err != nil {
				return fmt.Errorf("invalid agent file name %q: %w", e.Name(), err)
			}
			names = append(names, name)
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
	if err := cleanupLegacyHookArtifacts(opts.Root); err != nil {
		return err
	}

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

func processAgent(agentPath, executablePath, name string, remove bool) error {
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
		command := hookCommand(executablePath, name)
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
	match = isKapmCommand(entry.Command)
	return match, false
}

func hookCommand(executablePath, name string) string {
	return fmt.Sprintf("%s hook-handler --agent %s", strconv.Quote(executablePath), name)
}

func isKapmCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	commandPath, rest, ok := consumeCommandToken(command)
	if !ok {
		return false
	}
	if strings.HasPrefix(commandPath, "AGENT=") {
		commandPath, rest, ok = consumeCommandToken(rest)
		if !ok {
			return false
		}
	}
	normalizedPath := strings.ReplaceAll(commandPath, `\`, "/")
	if strings.Contains(normalizedPath, ".kiro/hooks/kapl") {
		return true
	}
	commandBase := filepath.Base(normalizedPath)
	if commandBase != "kapm" && commandBase != "kapm.exe" {
		return false
	}
	subcommand, _, ok := consumeCommandToken(rest)
	if !ok || subcommand != "hook-handler" {
		return false
	}
	return true
}

func consumeCommandToken(command string) (token, rest string, ok bool) {
	command = strings.TrimLeftFunc(command, unicode.IsSpace)
	if command == "" {
		return "", "", false
	}
	if command[0] != '"' && command[0] != '\'' {
		i := 0
		for i < len(command) && !unicode.IsSpace(rune(command[i])) {
			i++
		}
		return command[:i], command[i:], true
	}
	quote := command[0]
	var tokenBuilder strings.Builder
	escaped := false
	for i := 1; i < len(command); i++ {
		ch := command[i]
		if quote == '"' && escaped {
			tokenBuilder.WriteByte(ch)
			escaped = false
			continue
		}
		if quote == '"' && ch == '\\' {
			escaped = true
			continue
		}
		if ch == quote {
			return tokenBuilder.String(), command[i+1:], true
		}
		tokenBuilder.WriteByte(ch)
	}
	return "", "", false
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
