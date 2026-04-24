package agent

import (
	"path/filepath"

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

// LoggerBinaryPath returns "<root>/.kiro/hooks/kapl".
func LoggerBinaryPath(root string) string {
	return filepath.Join(root, paths.KiroDir, paths.HooksSubdir, "kapl")
}
