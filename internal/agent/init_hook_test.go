package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

func makeAgentsDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kiro", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeAgentJSON(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, ".kiro", "agents", name+".json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readAgentJSON(t *testing.T, root, name string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".kiro", "agents", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func runInitHook(t *testing.T, root string, remove bool, input string) (string, string, error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	err := InitHook(InitHookOptions{
		Root:   root,
		Remove: remove,
		In:     strings.NewReader(input),
		Out:    &outBuf,
		Err:    &errBuf,
	})
	return outBuf.String(), errBuf.String(), err
}

func TestInitHookAddsAllFiveEventsToFreshAgent(t *testing.T) {
	root := makeAgentsDir(t)
	writeAgentJSON(t, root, "coder", `{"name":"coder","description":"d","tools":["fs_read"],"allowedTools":["fs_read"]}`)

	out, _, err := runInitHook(t, root, false, "1\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "  ✓ coder") {
		t.Errorf("expected success output for coder, got: %q", out)
	}

	m := readAgentJSON(t, root, "coder")
	var hooksMap map[string][]json.RawMessage
	if err := json.Unmarshal(m["hooks"], &hooksMap); err != nil {
		t.Fatalf("hooks not valid JSON: %v", err)
	}

	for _, event := range apmconfig.HookEvents {
		entries, ok := hooksMap[event]
		if !ok || len(entries) != 1 {
			t.Errorf("event %q: want 1 entry, got %d", event, len(entries))
		}
	}

	for _, event := range []string{"preToolUse", "postToolUse"} {
		var entry map[string]string
		_ = json.Unmarshal(hooksMap[event][0], &entry)
		if entry["matcher"] != "*" {
			t.Errorf("event %q: want matcher=*, got %q", event, entry["matcher"])
		}
	}

	for _, event := range []string{"agentSpawn", "userPromptSubmit", "stop"} {
		var entry map[string]string
		_ = json.Unmarshal(hooksMap[event][0], &entry)
		if _, ok := entry["matcher"]; ok {
			t.Errorf("event %q: should not have matcher field", event)
		}
	}

	// Verify original fields preserved
	var name string
	_ = json.Unmarshal(m["name"], &name)
	if name != "coder" {
		t.Errorf("name field changed: %q", name)
	}
}

func TestInitHookPreservesUnknownTopLevelFields(t *testing.T) {
	root := makeAgentsDir(t)
	writeAgentJSON(t, root, "x", `{"name":"x","description":"d","tools":["fs_read"],"allowedTools":["fs_read"],"customField":{"a":1,"b":[true]},"keyboardShortcut":"ctrl+x"}`)

	_, _, err := runInitHook(t, root, false, "1\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := readAgentJSON(t, root, "x")

	if _, ok := m["customField"]; !ok {
		t.Error("customField was removed")
	}
	// Compare parsed values (marshalIndentedJSON reformats whitespace)
	var gotCustom, wantCustom interface{}
	_ = json.Unmarshal(m["customField"], &gotCustom)
	_ = json.Unmarshal([]byte(`{"a":1,"b":[true]}`), &wantCustom)
	gotBytes, _ := json.Marshal(gotCustom)
	wantBytes, _ := json.Marshal(wantCustom)
	if string(gotBytes) != string(wantBytes) {
		t.Errorf("customField changed: %s", m["customField"])
	}

	var ks string
	_ = json.Unmarshal(m["keyboardShortcut"], &ks)
	if ks != "ctrl+x" {
		t.Errorf("keyboardShortcut changed: %q", ks)
	}

	if _, ok := m["hooks"]; !ok {
		t.Error("hooks not added")
	}
}

func TestInitHookPreservesUserHookEntriesAndIsIdempotent(t *testing.T) {
	root := makeAgentsDir(t)
	writeAgentJSON(t, root, "y", `{"name":"y","description":"d","tools":["*"],"allowedTools":["*"],"hooks":{"postToolUse":[{"matcher":"fs_write","command":"my-custom.sh"}]}}`)

	// First run
	_, _, err := runInitHook(t, root, false, "1\n")
	if err != nil {
		t.Fatalf("first run error: %v", err)
	}

	m := readAgentJSON(t, root, "y")
	var hooksMap map[string][]json.RawMessage
	_ = json.Unmarshal(m["hooks"], &hooksMap)

	if len(hooksMap["postToolUse"]) != 2 {
		t.Fatalf("after first run: want 2 postToolUse entries, got %d", len(hooksMap["postToolUse"]))
	}
	var first map[string]string
	_ = json.Unmarshal(hooksMap["postToolUse"][0], &first)
	if first["command"] != "my-custom.sh" {
		t.Errorf("user entry not preserved: %v", first)
	}

	// Second run (idempotent)
	_, _, err = runInitHook(t, root, false, "1\n")
	if err != nil {
		t.Fatalf("second run error: %v", err)
	}

	m = readAgentJSON(t, root, "y")
	_ = json.Unmarshal(m["hooks"], &hooksMap)

	if len(hooksMap["postToolUse"]) != 2 {
		t.Fatalf("after second run: want 2 postToolUse entries, got %d (duplicate added)", len(hooksMap["postToolUse"]))
	}
}

func TestInitHookRemoveStripsOnlyKapmEntries(t *testing.T) {
	root := makeAgentsDir(t)
	writeAgentJSON(t, root, "y", `{"name":"y","description":"d","tools":["*"],"allowedTools":["*"],"hooks":{"postToolUse":[{"matcher":"fs_write","command":"my-custom.sh"}]}}`)

	// Add kapm entries
	_, _, err := runInitHook(t, root, false, "1\n")
	if err != nil {
		t.Fatalf("add error: %v", err)
	}

	// Remove kapm entries
	_, _, err = runInitHook(t, root, true, "1\n")
	if err != nil {
		t.Fatalf("remove error: %v", err)
	}

	m := readAgentJSON(t, root, "y")
	var hooksMap map[string][]json.RawMessage
	_ = json.Unmarshal(m["hooks"], &hooksMap)

	if len(hooksMap["postToolUse"]) != 1 {
		t.Fatalf("want 1 postToolUse entry, got %d", len(hooksMap["postToolUse"]))
	}
	var entry map[string]string
	_ = json.Unmarshal(hooksMap["postToolUse"][0], &entry)
	if entry["command"] != "my-custom.sh" {
		t.Errorf("user entry not preserved after remove: %v", entry)
	}

	for _, event := range []string{"agentSpawn", "userPromptSubmit", "preToolUse", "stop"} {
		if _, ok := hooksMap[event]; ok {
			t.Errorf("event %q should be removed but still present", event)
		}
	}
}

func TestInitHookRemoveDeletesEmptyHooksKey(t *testing.T) {
	root := makeAgentsDir(t)
	writeAgentJSON(t, root, "coder", `{"name":"coder","description":"d","tools":["fs_read"],"allowedTools":["fs_read"]}`)

	// Add then remove
	_, _, err := runInitHook(t, root, false, "1\n")
	if err != nil {
		t.Fatalf("add error: %v", err)
	}
	_, _, err = runInitHook(t, root, true, "1\n")
	if err != nil {
		t.Fatalf("remove error: %v", err)
	}

	m := readAgentJSON(t, root, "coder")
	if _, ok := m["hooks"]; ok {
		t.Error("hooks key should be deleted when empty")
	}
}

func TestInitHookNoopOnEmptyAgentsDir(t *testing.T) {
	root := makeAgentsDir(t)

	var out bytes.Buffer
	err := InitHook(InitHookOptions{
		Root: root,
		In:   strings.NewReader(""),
		Out:  &out,
		Err:  &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "No agents found") {
		t.Errorf("expected 'No agents found' message, got: %q", out.String())
	}
}

func TestInitHookSkipsMalformedAgentFile(t *testing.T) {
	root := makeAgentsDir(t)
	writeAgentJSON(t, root, "bad", `not valid json {`)
	writeAgentJSON(t, root, "good", `{"name":"good","description":"d","tools":["fs_read"],"allowedTools":["fs_read"]}`)

	_, errOut, err := runInitHook(t, root, false, "1,2\n")

	if err == nil {
		t.Error("expected error due to malformed agent, got nil")
	}

	if !strings.Contains(errOut, "bad") {
		t.Errorf("expected warning about bad in stderr, got: %q", errOut)
	}

	if !strings.Contains(err.Error(), "1 of 2 agents failed") {
		t.Errorf("expected '1 of 2 agents failed' in error, got: %v", err)
	}

	// good.json should have hooks added
	m := readAgentJSON(t, root, "good")
	if _, ok := m["hooks"]; !ok {
		t.Error("good.json should have hooks added")
	}

	// bad.json should be unchanged
	data, _ := os.ReadFile(filepath.Join(root, ".kiro", "agents", "bad.json"))
	if string(data) != `not valid json {` {
		t.Errorf("bad.json should be unchanged, got: %q", string(data))
	}
}

func TestInitHookWritesRelativeCommand(t *testing.T) {
	root := makeAgentsDir(t)
	writeAgentJSON(t, root, "coder", `{"name":"coder","description":"d","tools":["fs_read"],"allowedTools":["fs_read"]}`)

	_, _, err := runInitHook(t, root, false, "1\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := readAgentJSON(t, root, "coder")
	var hooksMap map[string][]json.RawMessage
	_ = json.Unmarshal(m["hooks"], &hooksMap)

	for _, event := range apmconfig.HookEvents {
		var entry map[string]string
		_ = json.Unmarshal(hooksMap[event][0], &entry)
		want := "AGENT=coder .kiro/hooks/kapl"
		if entry["command"] != want {
			t.Errorf("event %q command = %q, want %q", event, entry["command"], want)
		}
	}
}

func TestInitHookDeploysKapmLogger(t *testing.T) {
	root := makeAgentsDir(t)
	writeAgentJSON(t, root, "coder", `{"name":"coder","description":"d","tools":["fs_read"],"allowedTools":["fs_read"]}`)

	_, _, err := runInitHook(t, root, false, "1\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loggerPath := filepath.Join(root, ".kiro", "hooks", "kapl")
	info, err := os.Stat(loggerPath)
	if err != nil {
		t.Fatalf("kapl not found: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("kapl not executable: mode %o", info.Mode())
	}
}

func TestIsKapmEntryMatchesBothFormats(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{`{"command":"AGENT=coder .kiro/hooks/kapl"}`, true},
		{`{"command":"AGENT=coder '/usr/local/bin/kapm' hook-handler"}`, true},
		{`{"command":"my-custom.sh"}`, false},
		{`{"command":"not-kapl"}`, false},
	}
	for _, c := range cases {
		got, corrupt := isKapmEntry(json.RawMessage(c.raw))
		if corrupt {
			t.Errorf("isKapmEntry(%s): unexpected corrupt=true", c.raw)
		}
		if got != c.want {
			t.Errorf("isKapmEntry(%s) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestIsKapmEntry_MalformedJSONReportsCorrupt(t *testing.T) {
	raw := json.RawMessage("not-a-json")
	match, corrupt := isKapmEntry(raw)
	if match || !corrupt {
		t.Errorf("isKapmEntry(malformed) = (%v, %v), want (false, true)", match, corrupt)
	}
}

func TestAddKapmEntries_AbortsOnCorrupt(t *testing.T) {
	original := json.RawMessage("{broken")
	hooksMap := map[string][]json.RawMessage{
		"PreToolUse": {original},
	}
	err := addKapmEntries(hooksMap, "AGENT=x .kiro/hooks/kapl")
	if err == nil {
		t.Fatal("expected error for corrupt entry, got nil")
	}
	if !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("error should mention 'corrupt', got: %v", err)
	}
	// hooksMap must be byte-identical (no overwrite)
	if len(hooksMap["PreToolUse"]) != 1 || string(hooksMap["PreToolUse"][0]) != string(original) {
		t.Errorf("hooksMap was modified despite corrupt entry")
	}
}

func TestRemoveKapmEntries_PreservesCorrupt(t *testing.T) {
	corrupt := json.RawMessage("{broken")
	kapmRaw, _ := json.Marshal(map[string]string{"command": "AGENT=x .kiro/hooks/kapl"})
	hooksMap := map[string][]json.RawMessage{
		"PreToolUse": {corrupt, json.RawMessage(kapmRaw)},
	}
	removeKapmEntries(hooksMap)
	entries := hooksMap["PreToolUse"]
	if len(entries) != 1 {
		t.Fatalf("want 1 entry after remove, got %d", len(entries))
	}
	if string(entries[0]) != string(corrupt) {
		t.Errorf("corrupt entry not preserved: got %s", entries[0])
	}
}

func TestInitHookWrapsWriteFileError(t *testing.T) {
	root := makeAgentsDir(t)
	writeAgentJSON(t, root, "coder", `{"name":"coder","description":"d","tools":["fs_read"],"allowedTools":["fs_read"]}`)

	// Create hooks dir with no write permission
	hooksDir := filepath.Join(root, ".kiro", "hooks")
	if err := os.MkdirAll(hooksDir, 0o555); err != nil {
		t.Fatal(err)
	}

	_, _, err := runInitHook(t, root, false, "1\n")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "write kapl") {
		t.Errorf("error should contain 'write kapl', got: %v", err)
	}
}
