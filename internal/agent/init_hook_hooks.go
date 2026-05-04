package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

func processAgent(agentPath, executablePath, name string, remove bool) error {
	rawMap, _, err := readAgentRawJSON(agentPath)
	if err != nil {
		return fmt.Errorf("read agent json: %w", err)
	}
	return updateAgentHooks(rawMap, executablePath, name, remove, agentPath)
}

func updateAgentHooks(rawMap map[string]json.RawMessage, executablePath, name string, remove bool, agentPath string) error {
	hooksMap, err := decodeHooksMap(rawMap)
	if err != nil {
		return fmt.Errorf("decode hooks map: %w", err)
	}
	if err := applyHookChanges(hooksMap, executablePath, name, remove); err != nil {
		return fmt.Errorf("apply hook changes: %w", err)
	}
	compactHooksMap(hooksMap)
	if err := storeHooksMap(rawMap, hooksMap); err != nil {
		return fmt.Errorf("store hooks map: %w", err)
	}
	return writeAgentRawJSON(agentPath, rawMap)
}

func decodeHooksMap(rawMap map[string]json.RawMessage) (map[string][]json.RawMessage, error) {
	hooksMap := make(map[string][]json.RawMessage)
	if raw, ok := rawMap["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooksMap); err != nil {
			return nil, fmt.Errorf("unmarshal hooks: %w", err)
		}
	}
	return hooksMap, nil
}

func applyHookChanges(hooksMap map[string][]json.RawMessage, executablePath, name string, remove bool) error {
	if remove {
		removeKapmEntries(hooksMap)
		return nil
	}
	command := hookCommand(executablePath, name)
	return addKapmEntries(hooksMap, command)
}

func compactHooksMap(hooksMap map[string][]json.RawMessage) {
	// Clean up empty event arrays.
	for k, v := range hooksMap {
		if len(v) == 0 {
			delete(hooksMap, k)
		}
	}
}

func storeHooksMap(rawMap map[string]json.RawMessage, hooksMap map[string][]json.RawMessage) error {
	if len(hooksMap) == 0 {
		delete(rawMap, "hooks")
		return nil
	}
	hooksRaw, err := json.Marshal(hooksMap)
	if err != nil {
		return fmt.Errorf("marshal hooks: %w", err)
	}
	rawMap["hooks"] = json.RawMessage(hooksRaw)
	return nil
}

// isKapmEntry reports whether raw is a kapm-managed hook entry.
// A non-nil error signals corruption; callers must not overwrite the entry.
func isKapmEntry(raw json.RawMessage) (bool, error) {
	var entry struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return false, err
	}
	return isKapmCommand(entry.Command), nil
}

func hookCommand(executablePath, name string) string {
	return fmt.Sprintf("%s hook-handler --agent %s", shellQuote(executablePath), shellQuote(name))
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
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
	var tokenBuilder strings.Builder
	for i := 0; i < len(command); {
		ch := command[i]
		if unicode.IsSpace(rune(ch)) {
			return tokenBuilder.String(), command[i:], true
		}
		if ch == '\'' || ch == '"' {
			quote := ch
			i++
			closed := false
			for i < len(command) {
				ch = command[i]
				if quote == '"' && ch == '\\' {
					if i+1 >= len(command) {
						return "", "", false
					}
					tokenBuilder.WriteByte(command[i+1])
					i += 2
					continue
				}
				if ch == quote {
					i++
					closed = true
					break
				}
				tokenBuilder.WriteByte(ch)
				i++
			}
			if !closed {
				return "", "", false
			}
			continue
		}
		tokenBuilder.WriteByte(ch)
		i++
	}
	return tokenBuilder.String(), "", true
}

func removeKapmEntries(hooksMap map[string][]json.RawMessage) {
	for event, entries := range hooksMap {
		hooksMap[event] = filterHooks(entries, func(e json.RawMessage) bool {
			match, err := isKapmEntry(e)
			if err != nil {
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
			if _, err := isKapmEntry(e); err != nil {
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

		// Remove existing kapm entries for this event, then append new one.
		hooksMap[event] = append(filterHooks(hooksMap[event], func(e json.RawMessage) bool {
			match, _ := isKapmEntry(e) // corrupt already caught above
			return !match
		}), json.RawMessage(entryRaw))
	}
	return nil
}
