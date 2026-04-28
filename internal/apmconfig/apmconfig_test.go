package apmconfig

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateIdentifier(t *testing.T) {
	t.Parallel()

	valid := []struct {
		name, input, want string
	}{
		{"uuid", "8a388029-313c-4d9f-ba83-47750c287c0f", "8a388029-313c-4d9f-ba83-47750c287c0f"},
		{"dashed", "e2e-test", "e2e-test"},
		{"mixed", "a.b-c_1", "a.b-c_1"},
		{"trimmed", "  trimmed-name  ", "trimmed-name"},
	}
	for _, tt := range valid {
		t.Run("valid/"+tt.name, func(t *testing.T) {
			got, err := ValidateIdentifier(tt.input)
			if err != nil {
				t.Fatalf("ValidateIdentifier(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ValidateIdentifier(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}

	invalid := []struct {
		name, input string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"dot", "."},
		{"dotdot", ".."},
		{"absolute", "/abs"},
		{"forward-slash", "a/b"},
		{"backslash", `a\b`},
		{"parent-traversal", "../x"},
		{"unicode", "héllo"},
		{"bang", "abc!"},
	}
	for _, tt := range invalid {
		t.Run("invalid/"+tt.name, func(t *testing.T) {
			got, err := ValidateIdentifier(tt.input)
			if err == nil {
				t.Fatalf("ValidateIdentifier(%q) = %q, want error", tt.input, got)
			}
		})
	}
}

func TestMarshalIndentedJSON(t *testing.T) {
	t.Parallel()

	data, err := MarshalIndentedJSON(map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("MarshalIndentedJSON() error = %v", err)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatalf("MarshalIndentedJSON() missing trailing newline: %q", data)
	}
	if !strings.Contains(string(data), "\n  \"key\": \"value\"\n") {
		t.Fatalf("MarshalIndentedJSON() indent not 2-space: %q", data)
	}
}

func TestHookEventsOrderAndValues(t *testing.T) {
	t.Parallel()

	want := []string{"preToolUse", "postToolUse"}
	if len(HookEvents) != len(want) {
		t.Fatalf("HookEvents length = %d, want %d", len(HookEvents), len(want))
	}
	for i, v := range want {
		if HookEvents[i] != v {
			t.Errorf("HookEvents[%d] = %q, want %q", i, HookEvents[i], v)
		}
	}
}

func TestAgentConfig_OmitemptyResources(t *testing.T) {
	t.Parallel()

	cfg := AgentConfig{
		Name:         "x",
		Description:  "d",
		Prompt:       "p",
		Tools:        []string{},
		AllowedTools: []string{},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(data), "resources") {
		t.Fatalf("marshalled config contains %q: %s", "resources", data)
	}
}
