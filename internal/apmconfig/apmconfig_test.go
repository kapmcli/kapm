package apmconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

func TestLoadStrictYAMLManifest(t *testing.T) {
	t.Parallel()

	type manifest struct {
		Name string `yaml:"name"`
	}

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		path := writeManifestForTest(t, "name: kapm\n")
		got, ok, err := LoadStrictYAMLManifest[manifest](path, os.ReadFile, nil, nil)
		if err != nil {
			t.Fatalf("LoadStrictYAMLManifest() error = %v", err)
		}
		if !ok {
			t.Fatal("LoadStrictYAMLManifest() ok = false, want true")
		}
		if got.Name != "kapm" {
			t.Fatalf("manifest.Name = %q, want %q", got.Name, "kapm")
		}
	})

	t.Run("missing", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "apm.yml")
		_, ok, err := LoadStrictYAMLManifest[manifest](path, os.ReadFile, nil, nil)
		if err != nil {
			t.Fatalf("LoadStrictYAMLManifest() error = %v", err)
		}
		if ok {
			t.Fatal("LoadStrictYAMLManifest() ok = true, want false")
		}
	})

	t.Run("read error wrapper", func(t *testing.T) {
		t.Parallel()

		path := writeManifestForTest(t, "name: kapm\n")
		readErr := errors.New("boom")
		_, _, err := LoadStrictYAMLManifest[manifest](
			path,
			func(string) ([]byte, error) { return nil, readErr },
			nil,
			func(path string, err error) error { return errors.Join(errors.New("wrapped read "+path), err) },
		)
		if !errors.Is(err, readErr) {
			t.Fatalf("LoadStrictYAMLManifest() error = %v, want wrapped read error", err)
		}
		if !strings.Contains(err.Error(), "wrapped read "+path) {
			t.Fatalf("LoadStrictYAMLManifest() error = %v, want wrapper context", err)
		}
	})

	t.Run("size limit", func(t *testing.T) {
		t.Parallel()

		path := writeManifestForTest(t, strings.Repeat("x", MaxManifestBytes+1))
		_, _, err := LoadStrictYAMLManifest[manifest](path, os.ReadFile, nil, nil)
		if err == nil {
			t.Fatal("LoadStrictYAMLManifest() error = nil, want size error")
		}
		if !strings.Contains(err.Error(), "too large") {
			t.Fatalf("LoadStrictYAMLManifest() error = %v, want size limit", err)
		}
	})

	t.Run("unknown field", func(t *testing.T) {
		t.Parallel()

		path := writeManifestForTest(t, "unknown: value\n")
		_, _, err := LoadStrictYAMLManifest[manifest](path, os.ReadFile, nil, nil)
		if err == nil {
			t.Fatal("LoadStrictYAMLManifest() error = nil, want unknown field error")
		}
		if !strings.Contains(err.Error(), "parse apm.yml") {
			t.Fatalf("LoadStrictYAMLManifest() error = %v, want parse context", err)
		}
	})

	t.Run("stat error wrapper", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "apm.yml")
		if err := os.Symlink(path, path); err != nil {
			t.Skipf("Symlink() unavailable: %v", err)
		}
		_, _, err := LoadStrictYAMLManifest[manifest](
			path,
			os.ReadFile,
			func(path string, err error) error { return errors.Join(errors.New("wrapped stat "+path), err) },
			nil,
		)
		if err == nil {
			t.Fatal("LoadStrictYAMLManifest() error = nil, want stat error")
		}
		if !strings.Contains(err.Error(), "wrapped stat "+path) {
			t.Fatalf("LoadStrictYAMLManifest() error = %v, want wrapper context", err)
		}
	})
}

func writeManifestForTest(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "apm.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}

func TestHookEventsOrderAndValues(t *testing.T) {
	t.Parallel()

	want := []string{"agentSpawn", "preToolUse", "postToolUse", "stop"}
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
