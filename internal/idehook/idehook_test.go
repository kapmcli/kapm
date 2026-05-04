package idehook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitInstallsIDEHooks(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "kapm binary")

	var out strings.Builder
	if err := Init(Options{Root: root, Executable: executable, Out: &out}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	for _, spec := range hookSpecs {
		path := hookPath(root, spec.FileName)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", path, err)
		}
		var got hookFile
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal(%s): %v", path, err)
		}
		if got.Name != spec.Name {
			t.Errorf("%s name = %q, want %q", spec.FileName, got.Name, spec.Name)
		}
		if !got.Enabled {
			t.Errorf("%s enabled = false, want true", spec.FileName)
		}
		if got.When.Type != spec.WhenType {
			t.Errorf("%s when.type = %q, want %q", spec.FileName, got.When.Type, spec.WhenType)
		}
		if len(spec.ToolTypes) == 0 {
			if len(got.When.ToolTypes) != 0 {
				t.Errorf("%s when.toolTypes = %v, want omitted", spec.FileName, got.When.ToolTypes)
			}
		} else if strings.Join(got.When.ToolTypes, ",") != strings.Join(spec.ToolTypes, ",") {
			t.Errorf("%s when.toolTypes = %v, want %v", spec.FileName, got.When.ToolTypes, spec.ToolTypes)
		}
		if len(spec.Patterns) == 0 {
			if len(got.When.Patterns) != 0 {
				t.Errorf("%s when.patterns = %v, want omitted", spec.FileName, got.When.Patterns)
			}
		} else if strings.Join(got.When.Patterns, ",") != strings.Join(spec.Patterns, ",") {
			t.Errorf("%s when.patterns = %v, want %v", spec.FileName, got.When.Patterns, spec.Patterns)
		}
		wantCommand := hookCommand(executable, spec.Event)
		if got.Then.Type != "runCommand" || got.Then.Command != wantCommand {
			t.Errorf("%s then = %#v, want runCommand %q", spec.FileName, got.Then, wantCommand)
		}
	}

	if !strings.Contains(out.String(), "kapm-manual-hook-event.kiro.hook") {
		t.Fatalf("output = %q, want installed hook path", out.String())
	}
}

func TestHookCommandShellQuotesArguments(t *testing.T) {
	command := hookCommand(`/tmp/kapm $(touch pwn)'bin`, "preToolUse")
	want := `'/tmp/kapm $(touch pwn)'\''bin' ide-hook-handler --agent 'ide' --event 'preToolUse'`
	if command != want {
		t.Fatalf("hookCommand() = %q, want %q", command, want)
	}
}

func TestInitRemoveDeletesOnlyKapmIDEHooks(t *testing.T) {
	root := t.TempDir()
	if err := Init(Options{Root: root, Executable: filepath.Join(root, "kapm"), Out: &strings.Builder{}}); err != nil {
		t.Fatalf("install: %v", err)
	}
	obsoletePath := filepath.Join(root, ".kiro", "hooks", "kapm-file-saved.kiro.hook")
	obsoleteManualPath := filepath.Join(root, ".kiro", "hooks", "kapm-manual-dump.kiro.hook")
	if err := os.WriteFile(obsoletePath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(obsolete): %v", err)
	}
	if err := os.WriteFile(obsoleteManualPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(obsolete manual): %v", err)
	}
	customPath := filepath.Join(root, ".kiro", "hooks", "custom.kiro.hook")
	if err := os.WriteFile(customPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(custom): %v", err)
	}

	var out strings.Builder
	if err := Init(Options{Root: root, Remove: true, Out: &out}); err != nil {
		t.Fatalf("remove: %v", err)
	}

	for _, spec := range hookSpecs {
		if _, err := os.Stat(hookPath(root, spec.FileName)); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed, stat err = %v", spec.FileName, err)
		}
	}
	if _, err := os.Stat(customPath); err != nil {
		t.Fatalf("custom hook should remain: %v", err)
	}
	if _, err := os.Stat(obsoletePath); !os.IsNotExist(err) {
		t.Fatalf("obsolete hook should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(obsoleteManualPath); !os.IsNotExist(err) {
		t.Fatalf("obsolete manual hook should be removed, stat err = %v", err)
	}
	if !strings.Contains(out.String(), "kapm-stop.kiro.hook") {
		t.Fatalf("output = %q, want removed hook path", out.String())
	}
}

func TestInitInstallRemovesObsoleteIDEHooks(t *testing.T) {
	root := t.TempDir()
	obsoletePath := filepath.Join(root, ".kiro", "hooks", "kapm-file-saved.kiro.hook")
	obsoleteManualPath := filepath.Join(root, ".kiro", "hooks", "kapm-manual-dump.kiro.hook")
	if err := os.MkdirAll(filepath.Dir(obsoletePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(obsoletePath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(obsolete): %v", err)
	}
	if err := os.WriteFile(obsoleteManualPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(obsolete manual): %v", err)
	}

	if err := Init(Options{Root: root, Executable: filepath.Join(root, "kapm"), Out: &strings.Builder{}}); err != nil {
		t.Fatalf("install: %v", err)
	}

	if _, err := os.Stat(obsoletePath); !os.IsNotExist(err) {
		t.Fatalf("obsolete hook should be removed on install, stat err = %v", err)
	}
	if _, err := os.Stat(obsoleteManualPath); !os.IsNotExist(err) {
		t.Fatalf("obsolete manual hook should be removed on install, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".kiro", "hooks", "kapm-file-edited.kiro.hook")); err != nil {
		t.Fatalf("fileEdited hook should exist: %v", err)
	}
}

func TestInitRemoveDeletesEmptyHooksDir(t *testing.T) {
	root := t.TempDir()
	if err := Init(Options{Root: root, Executable: filepath.Join(root, "kapm"), Out: &strings.Builder{}}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := Init(Options{Root: root, Remove: true, Out: &strings.Builder{}}); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, ".kiro", "hooks")); !os.IsNotExist(err) {
		t.Fatalf("empty hooks dir should be removed, stat err = %v", err)
	}
}
