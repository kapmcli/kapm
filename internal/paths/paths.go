// Package paths provides kapm-managed path constants for .kiro/, .kapm/, and .apm/
// directories. Callers use these with filepath.Join to build concrete
// filesystem paths.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

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

const ideStorageSuffix = "Kiro/User/globalStorage/kiro.kiroagent"

// IDEBaseDir returns the default Kiro IDE globalStorage directory for the
// current OS. Returns empty string if the home directory cannot be determined.
func IDEBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", ideStorageSuffix)
	case "linux":
		return filepath.Join(home, ".config", ideStorageSuffix)
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			return ""
		}
		return filepath.Join(appdata, ideStorageSuffix)
	default:
		return ""
	}
}
