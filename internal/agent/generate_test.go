package agent_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kapmcli/kapm/internal/agent"
)

var testKnownTools = []string{"fs_read", "fs_write", "execute_bash", "code", "grep", "glob", "thinking"}

type generatedAgentConfig struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Prompt       string   `json:"prompt"`
	Model        string   `json:"model,omitempty"`
	Tools        []string `json:"tools"`
	AllowedTools []string `json:"allowedTools"`
	Resources    []string `json:"resources,omitempty"`
}

func TestGenerateBasic(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	var out bytes.Buffer

	err := agent.Generate(agent.GenerateOptions{
		Root: root,
		In:   buildInput("test-agent", "A test agent", "1", "1", "", "", "file://AGENTS.md", ""),
		Out:  &out,
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	config, raw := readGeneratedAgent(t, root, "test-agent")
	if config.Name != "test-agent" {
		t.Fatalf("Name = %q, want %q", config.Name, "test-agent")
	}
	if config.Description != "A test agent" {
		t.Fatalf("Description = %q, want %q", config.Description, "A test agent")
	}
	if config.Prompt != "file://../agent-prompts/test-agent.md" {
		t.Fatalf("Prompt = %q, want %q", config.Prompt, "file://../agent-prompts/test-agent.md")
	}
	if config.Model != "claude-sonnet-4-5" {
		t.Fatalf("Model = %q, want %q", config.Model, "claude-sonnet-4-5")
	}
	if !reflect.DeepEqual(config.Tools, testKnownTools) {
		t.Fatalf("Tools = %#v, want %#v", config.Tools, testKnownTools)
	}
	if !reflect.DeepEqual(config.AllowedTools, testKnownTools) {
		t.Fatalf("AllowedTools = %#v, want %#v", config.AllowedTools, testKnownTools)
	}
	if !reflect.DeepEqual(config.Resources, []string{"skill://.kiro/skills/**/SKILL.md", "file://AGENTS.md"}) {
		t.Fatalf("Resources = %#v, want %#v", config.Resources, []string{"skill://.kiro/skills/**/SKILL.md", "file://AGENTS.md"})
	}
	if _, ok := raw["toolsSettings"]; ok {
		t.Fatal("toolsSettings unexpectedly present in generated JSON")
	}

	promptPath := filepath.Join(root, ".kiro", "agent-prompts", "test-agent.md")
	promptData, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", promptPath, err)
	}
	if string(promptData) != "# test-agent\n" {
		t.Fatalf("prompt content = %q, want %q", string(promptData), "# test-agent\n")
	}
}

func TestGenerateExistingFails(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, ".kiro", "agents", "test-agent.json")
	writeFileForTest(t, path, []byte("old\n"))

	err := agent.Generate(agent.GenerateOptions{
		Root: root,
		In:   buildInput("test-agent"),
		Out:  &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("Generate() error = nil, want existing file error")
	}
	if !strings.Contains(err.Error(), `agent "test-agent" already exists; use --force to overwrite`) {
		t.Fatalf("Generate() error = %v, want already exists error", err)
	}
}

func TestGenerateForce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFileForTest(t, filepath.Join(root, ".kiro", "agents", "force-agent.json"), []byte("old\n"))
	writeFileForTest(t, filepath.Join(root, ".kiro", "agent-prompts", "force-agent.md"), []byte("old prompt\n"))

	err := agent.Generate(agent.GenerateOptions{
		Root:  root,
		Force: true,
		In:    buildInput("force-agent", "Force agent", "2", "1", "", "", ""),
		Out:   &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	config, _ := readGeneratedAgent(t, root, "force-agent")
	if config.Description != "Force agent" {
		t.Fatalf("Description = %q, want %q", config.Description, "Force agent")
	}
	if config.Model != "claude-sonnet-4-5-20251001" {
		t.Fatalf("Model = %q, want %q", config.Model, "claude-sonnet-4-5-20251001")
	}
}

func TestGenerateForcePreservesExistingFilesOnLaterValidationError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	agentPath := filepath.Join(root, ".kiro", "agents", "force-agent.json")
	promptPath := filepath.Join(root, ".kiro", "agent-prompts", "force-agent.md")
	writeFileForTest(t, agentPath, []byte("old\n"))
	writeFileForTest(t, promptPath, []byte("old prompt\n"))

	err := agent.Generate(agent.GenerateOptions{
		Root:  root,
		Force: true,
		In:    buildInput("force-agent", "Force agent", "7", "", "", ""),
		Out:   &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("Generate() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "custom model cannot be empty") {
		t.Fatalf("Generate() error = %v, want custom model validation error", err)
	}

	agentData, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", agentPath, err)
	}
	if string(agentData) != "old\n" {
		t.Fatalf("agent file = %q, want preserved old content", string(agentData))
	}

	promptData, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", promptPath, err)
	}
	if string(promptData) != "old prompt\n" {
		t.Fatalf("prompt file = %q, want preserved old content", string(promptData))
	}
}

func TestGenerateCustomModel(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := agent.Generate(agent.GenerateOptions{
		Root: root,
		In:   buildInput("custom-agent", "Custom agent", "7", "my-custom-model", "1", "", "", ""),
		Out:  &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	config, _ := readGeneratedAgent(t, root, "custom-agent")
	if config.Model != "my-custom-model" {
		t.Fatalf("Model = %q, want %q", config.Model, "my-custom-model")
	}
}

func TestGenerateDefaultResources(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := agent.Generate(agent.GenerateOptions{
		Root: root,
		In:   buildInput("default-resources", "Default resources", "1", "1", "", "", ""),
		Out:  &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	config, _ := readGeneratedAgent(t, root, "default-resources")
	want := []string{"skill://.kiro/skills/**/SKILL.md"}
	if !slices.Equal(config.Resources, want) {
		t.Fatalf("Resources = %v, want %v", config.Resources, want)
	}
}

func TestGenerateTrimsName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := agent.Generate(agent.GenerateOptions{
		Root: root,
		In:   buildInput("  trimmed-agent  ", "Trimmed agent", "1", "", "", ""),
		Out:  &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	config, _ := readGeneratedAgent(t, root, "trimmed-agent")
	if config.Name != "trimmed-agent" {
		t.Fatalf("Name = %q, want %q", config.Name, "trimmed-agent")
	}
}

func TestGenerateRejectsSymlinkedKiroParent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	external := t.TempDir()
	kiroPath := filepath.Join(root, ".kiro")
	if err := os.Symlink(external, kiroPath); err != nil {
		t.Fatalf("Symlink(%q, %q): %v", external, kiroPath, err)
	}

	err := agent.Generate(agent.GenerateOptions{
		Root: root,
		In:   buildInput("test-agent", "A test agent", "1", "", "", ""),
		Out:  &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("Generate() error = nil, want symlink rejection")
	}
	if !strings.Contains(err.Error(), `path `) || !strings.Contains(err.Error(), `must not be a symlink`) {
		t.Fatalf("Generate() error = %v, want symlink rejection", err)
	}
}

func buildInput(lines ...string) io.Reader {
	return strings.NewReader(strings.Join(lines, "\n") + "\n")
}

func readGeneratedAgent(t *testing.T, root, name string) (generatedAgentConfig, map[string]any) {
	t.Helper()

	path := filepath.Join(root, ".kiro", "agents", name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}

	var config generatedAgentConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("Unmarshal(%q): %v", path, err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw(%q): %v", path, err)
	}

	return config, raw
}

func writeFileForTest(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
