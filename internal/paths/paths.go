// Package paths provides kapm-managed path constants for .kiro/, .kapm/, and .apm/
// directories. Callers use these with filepath.Join to build concrete
// filesystem paths.
package paths

const (
	// .kiro hierarchy
	KiroDir         = ".kiro"
	AgentsSubdir    = "agents"
	AgentPromptsDir = "agent-prompts"
	HooksSubdir     = "hooks"
	SkillsSubdir    = "skills"
	PromptsSubdir   = "prompts"
	SteeringSubdir  = "steering"
	SettingsSubdir  = "settings"

	// .kapm hierarchy
	KapmDir    = ".kapm"
	LogsSubdir = "logs"

	// .apm hierarchy
	APMSubdir     = ".apm"
	APMModulesDir = "apm_modules"

	// file names
	APMManifest = "apm.yml"
	MCPFile     = "mcp.json"
)
