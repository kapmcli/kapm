//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runKapm invokes kapm with args in root, feeding stdin and capturing output.
func runKapm(t *testing.T, root, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	kapm := binary(t)
	cmd := exec.Command(kapm, args...)
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(stdin)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// TestAgentGenerateWritesExpectedArtifacts exercises the interactive
// `kapm agent generate` command end-to-end.
// It feeds default-accepting input lines and asserts the two on-disk artifacts
// (agent JSON, agent prompt markdown) are created with the expected content.
func TestAgentGenerateWritesExpectedArtifacts(t *testing.T) {
	root := t.TempDir()

	// Prompt order (see internal/agent/generate.go):
	//   name, description, model (Select), preset (Select),
	//   tools (MultiSelect), allowedTools (MultiSelect),
	//   additional resources (MultiInput, blank line to finish).
	input := strings.Join([]string{
		"tester",        // name
		"desc for test", // description
		"",              // model: accept default
		"",              // preset: accept default
		"",              // tools: accept default
		"",              // allowedTools: accept default
		"",              // additional resources: blank line ends MultiInput
	}, "\n") + "\n"

	_, stderr, err := runKapm(t, root, input, "agent", "generate")
	if err != nil {
		t.Fatalf("kapm agent generate failed: %v\nstderr: %s", err, stderr)
	}

	agentFile := filepath.Join(root, ".kiro", "agents", "tester.json")
	promptFile := filepath.Join(root, ".kiro", "agent-prompts", "tester.md")

	data, err := os.ReadFile(agentFile)
	if err != nil {
		t.Fatalf("read agent file: %v", err)
	}
	var cfg struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Prompt      string   `json:"prompt"`
		Resources   []string `json:"resources"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse agent json: %v", err)
	}
	if cfg.Name != "tester" || cfg.Description != "desc for test" {
		t.Fatalf("unexpected agent config: %+v", cfg)
	}
	if cfg.Prompt != "file://../agent-prompts/tester.md" {
		t.Fatalf("unexpected prompt reference: %q", cfg.Prompt)
	}

	promptData, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatalf("read prompt file: %v", err)
	}
	if !strings.Contains(string(promptData), "# tester") {
		t.Fatalf("prompt missing title: %q", promptData)
	}
}

// TestInitHookAddAndRemoveIsIdempotent exercises `kapm init-hook` against a
// generated agent: adding installs hook entries, adding again is a no-op on
// entry count, and --remove strips them cleanly.
func TestInitHookAddAndRemoveIsIdempotent(t *testing.T) {
	root := t.TempDir()

	// Seed one agent via `kapm agent generate`.
	genInput := strings.Join([]string{"hooked", "d", "", "", "", "", ""}, "\n") + "\n"
	if _, stderr, err := runKapm(t, root, genInput, "agent", "generate"); err != nil {
		t.Fatalf("seed agent: %v\n%s", err, stderr)
	}

	// init-hook add: MultiSelect(defaultAll=true), Enter to accept all.
	if _, stderr, err := runKapm(t, root, "\n", "init-hook"); err != nil {
		t.Fatalf("init-hook add: %v\n%s", err, stderr)
	}
	hooks1 := readHooks(t, root, "hooked")
	if len(hooks1) == 0 {
		t.Fatal("expected hooks after init-hook add")
	}
	for _, event := range []string{"agentSpawn", "userPromptSubmit", "preToolUse", "postToolUse", "stop"} {
		if len(hooks1[event]) != 1 {
			t.Fatalf("event %q: expected 1 entry, got %d", event, len(hooks1[event]))
		}
	}
	// init-hook add again: must not duplicate entries.
	if _, stderr, err := runKapm(t, root, "\n", "init-hook"); err != nil {
		t.Fatalf("init-hook add (second): %v\n%s", err, stderr)
	}
	hooks2 := readHooks(t, root, "hooked")
	for event, entries := range hooks2 {
		if len(entries) != 1 {
			t.Fatalf("event %q not idempotent: got %d entries", event, len(entries))
		}
	}

	// init-hook --remove: all kapm entries stripped.
	if _, stderr, err := runKapm(t, root, "\n", "init-hook", "--remove"); err != nil {
		t.Fatalf("init-hook remove: %v\n%s", err, stderr)
	}
	hooks3 := readHooks(t, root, "hooked")
	if len(hooks3) != 0 {
		t.Fatalf("expected no hook events after remove, got: %v", hooks3)
	}
}

// readHooks returns the "hooks" map from .kiro/agents/<name>.json.
// Missing or empty hooks yield an empty map.
func readHooks(t *testing.T, root, name string) map[string][]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".kiro", "agents", name+".json"))
	if err != nil {
		t.Fatalf("read agent: %v", err)
	}
	var wrapper struct {
		Hooks map[string][]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		t.Fatalf("parse agent json: %v", err)
	}
	if wrapper.Hooks == nil {
		return map[string][]json.RawMessage{}
	}
	return wrapper.Hooks
}
