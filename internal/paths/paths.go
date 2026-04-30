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
	SessionsSubdir  = "sessions"
	CLISubdir       = "cli"

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

const cliDBFile = "kiro-cli/data.sqlite3"

// CLIDataPath returns the path to the v1 SQLite database file for the current OS.
// Returns empty string if the home directory cannot be determined or on Windows (TODO).
func CLIDataPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", cliDBFile)
	case "linux":
		return filepath.Join(home, ".local", "share", cliDBFile)
	default:
		return ""
	}
}

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
