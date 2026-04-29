package paths_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kapmcli/kapm/internal/paths"
)

func TestConstants(t *testing.T) {
	cases := []struct{ name, got, want string }{
		{"KiroDir", paths.KiroDir, ".kiro"},
		{"KapmDir", paths.KapmDir, ".kapm"},
		{"AgentsSubdir", paths.AgentsSubdir, "agents"},
		{"AgentPromptsDir", paths.AgentPromptsDir, "agent-prompts"},
		{"HooksSubdir", paths.HooksSubdir, "hooks"},
		{"LogsSubdir", paths.LogsSubdir, "logs"},
		{"SkillsSubdir", paths.SkillsSubdir, "skills"},
		{"PromptsSubdir", paths.PromptsSubdir, "prompts"},
		{"SettingsSubdir", paths.SettingsSubdir, "settings"},
		{"APMManifest", paths.APMManifest, "apm.yml"},
		{"MCPFile", paths.MCPFile, "mcp.json"},
		{"APMModulesDir", paths.APMModulesDir, "apm_modules"},
		{"APMSubdir", paths.APMSubdir, ".apm"},
		{"SteeringSubdir", paths.SteeringSubdir, "steering"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestIDEBaseDir(t *testing.T) {
	got := paths.IDEBaseDir()
	if got == "" {
		t.Fatal("IDEBaseDir() returned empty string")
	}
	if !filepath.IsAbs(got) {
		t.Errorf("IDEBaseDir() = %q, want absolute path", got)
	}

	suffixByOS := map[string]string{
		"darwin":  "Library/Application Support/Kiro/User/globalStorage/kiro.kiroagent",
		"linux":   ".config/Kiro/User/globalStorage/kiro.kiroagent",
		"windows": "Kiro/User/globalStorage/kiro.kiroagent",
	}
	if want, ok := suffixByOS[runtime.GOOS]; ok {
		if !strings.Contains(got, want) {
			t.Errorf("IDEBaseDir() = %q, want to contain %q", got, want)
		}
	}
}
