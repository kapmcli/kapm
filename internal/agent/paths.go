package agent

import (
	"path/filepath"
	"runtime"

	"github.com/kapmcli/kapm/internal/paths"
)

// AgentFile returns "<root>/.kiro/agents/<name>.json".
func AgentFile(root, name string) string {
	return filepath.Join(root, paths.KiroDir, paths.AgentsSubdir, name+".json")
}

// AgentPromptFile returns "<root>/.kiro/agent-prompts/<name>.md".
func AgentPromptFile(root, name string) string {
	return filepath.Join(root, paths.KiroDir, paths.AgentPromptsDir, name+".md")
}

// HooksDir returns "<root>/.kiro/hooks".
func HooksDir(root string) string {
	return filepath.Join(root, paths.KiroDir, paths.HooksSubdir)
}

func loggerBinaryNameForGOOS(goos string) string {
	if goos == "windows" {
		return "kapl.exe"
	}
	return "kapl"
}

func loggerBinaryName() string {
	return loggerBinaryNameForGOOS(runtime.GOOS)
}

// LoggerBinaryPath returns the installed hook logger path under
// "<root>/.kiro/hooks". The binary is named "kapl.exe" on Windows and "kapl"
// elsewhere.
func LoggerBinaryPath(root string) string {
	return filepath.Join(root, paths.KiroDir, paths.HooksSubdir, loggerBinaryName())
}
