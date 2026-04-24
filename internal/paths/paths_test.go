package paths_test

import (
	"testing"

	"github.com/kapmcli/kapm/internal/paths"
)

func TestConstants(t *testing.T) {
	cases := []struct{ name, got, want string }{
		{"KiroDir", paths.KiroDir, ".kiro"},
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
