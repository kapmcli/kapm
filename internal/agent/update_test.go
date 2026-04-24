package agent_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kapmcli/kapm/internal/agent"
)

func TestUpdateBasic(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeAgentJSON(t, root, "my-agent", map[string]any{
		"name":             "my-agent",
		"description":      "My agent",
		"prompt":           "file://../agent-prompts/my-agent.md",
		"model":            "claude-sonnet-4-5",
		"tools":            []string{"fs_read", "fs_write"},
		"allowedTools":     []string{"fs_read", "fs_write"},
		"toolsSettings":    map[string]any{"shell": map[string]any{"allowedCommands": []string{"git"}}},
		"keyboardShortcut": "ctrl+m",
	})

	var out bytes.Buffer
	err := agent.Update(agent.UpdateOptions{
		Root: root,
		Name: "my-agent",
		In:   buildInput("claude-opus-4-5", "", "", ""),
		Out:  &out,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	config, raw := readGeneratedAgent(t, root, "my-agent")
	if config.Model != "claude-opus-4-5" {
		t.Fatalf("Model = %q, want %q", config.Model, "claude-opus-4-5")
	}

	expectedToolsSettings := map[string]any{"shell": map[string]any{"allowedCommands": []any{"git"}}}
	if !reflect.DeepEqual(raw["toolsSettings"], expectedToolsSettings) {
		t.Fatalf("toolsSettings = %#v, want %#v", raw["toolsSettings"], expectedToolsSettings)
	}
	if raw["keyboardShortcut"] != "ctrl+m" {
		t.Fatalf("keyboardShortcut = %#v, want %q", raw["keyboardShortcut"], "ctrl+m")
	}
}

func TestUpdateNoChange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeAgentJSON(t, root, "unchanged-agent", map[string]any{
		"name":         "unchanged-agent",
		"description":  "Unchanged agent",
		"prompt":       "file://../agent-prompts/unchanged-agent.md",
		"model":        "claude-sonnet-4-5",
		"tools":        []string{"fs_read", "fs_write"},
		"allowedTools": []string{"fs_read", "fs_write"},
		"resources":    []string{"file://AGENTS.md"},
	})

	path := filepath.Join(root, ".kiro", "agents", "unchanged-agent.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}

	err = agent.Update(agent.UpdateOptions{
		Root: root,
		Name: "unchanged-agent",
		In:   buildInput("", "", "", ""),
		Out:  &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("file content changed unexpectedly\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestUpdateMissingFile(t *testing.T) {
	t.Parallel()

	err := agent.Update(agent.UpdateOptions{
		Root: t.TempDir(),
		Name: "missing-agent",
		In:   buildInput(""),
		Out:  &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("Update() error = nil, want missing file error")
	}
	if !strings.Contains(err.Error(), `agent "missing-agent" does not exist`) {
		t.Fatalf("Update() error = %v, want missing file error", err)
	}
}

func TestUpdatePreservesUnknownFields(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeAgentJSON(t, root, "custom-agent", map[string]any{
		"name":         "custom-agent",
		"description":  "Custom agent",
		"prompt":       "file://../agent-prompts/custom-agent.md",
		"model":        "claude-sonnet-4-5",
		"tools":        []string{"fs_read"},
		"allowedTools": []string{"fs_read"},
		"customField":  "customValue",
	})

	err := agent.Update(agent.UpdateOptions{
		Root: root,
		Name: "custom-agent",
		In:   buildInput("gpt-4o", "", "", ""),
		Out:  &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	_, raw := readGeneratedAgent(t, root, "custom-agent")
	if raw["customField"] != "customValue" {
		t.Fatalf("customField = %#v, want %q", raw["customField"], "customValue")
	}
}

func TestUpdateResources(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeAgentJSON(t, root, "resource-agent", map[string]any{
		"name":         "resource-agent",
		"description":  "Resource agent",
		"prompt":       "file://../agent-prompts/resource-agent.md",
		"model":        "claude-sonnet-4-5",
		"tools":        []string{"fs_read"},
		"allowedTools": []string{"fs_read"},
	})

	err := agent.Update(agent.UpdateOptions{
		Root: root,
		Name: "resource-agent",
		In:   buildInput("", "", "", "skill://my-skill", ""),
		Out:  &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	config, _ := readGeneratedAgent(t, root, "resource-agent")
	if !reflect.DeepEqual(config.Resources, []string{"skill://my-skill"}) {
		t.Fatalf("Resources = %#v, want %#v", config.Resources, []string{"skill://my-skill"})
	}
}

func TestUpdateResourcesEmptySlicePreservedAsNoChange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeAgentJSON(t, root, "empty-resources-agent", map[string]any{
		"name":         "empty-resources-agent",
		"description":  "Empty resources agent",
		"prompt":       "file://../agent-prompts/empty-resources-agent.md",
		"model":        "claude-sonnet-4-5",
		"tools":        []string{"fs_read"},
		"allowedTools": []string{"fs_read"},
		"resources":    []string{},
	})

	path := filepath.Join(root, ".kiro", "agents", "empty-resources-agent.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}

	var out bytes.Buffer
	err = agent.Update(agent.UpdateOptions{
		Root: root,
		Name: "empty-resources-agent",
		In:   buildInput("", "", "", ""),
		Out:  &out,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if !strings.Contains(out.String(), "No changes.") {
		t.Fatalf("output = %q, want to contain %q", out.String(), "No changes.")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("file content changed unexpectedly\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func writeAgentJSON(t *testing.T, root, name string, value map[string]any) {
	t.Helper()

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(): %v", err)
	}
	data = append(data, '\n')

	writeFileForTest(t, filepath.Join(root, ".kiro", "agents", name+".json"), data)
}
