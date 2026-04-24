// Package paths provides kapm-managed path constants for .kiro/ and .apm/
// directories. Callers use these with filepath.Join to build concrete
// filesystem paths.
package paths

const (
	// .kiro hierarchy
	KiroDir         = ".kiro"
	AgentsSubdir    = "agents"
	AgentPromptsDir = "agent-prompts"
	HooksSubdir     = "hooks"
	LogsSubdir      = "logs"
	SkillsSubdir    = "skills"
	PromptsSubdir   = "prompts"
	SteeringSubdir  = "steering"
	SettingsSubdir  = "settings"

	// .apm hierarchy
	APMSubdir     = ".apm"
	APMModulesDir = "apm_modules"

	// file names
	APMManifest = "apm.yml"
	MCPFile     = "mcp.json"
)
