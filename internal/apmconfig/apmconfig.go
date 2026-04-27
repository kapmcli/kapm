// Package apmconfig holds shared constants and helpers for kapm's agent configuration.
package apmconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// DefaultAgentTools is the canonical list of tools exposed to agents.
var DefaultAgentTools = []string{"fs_read", "fs_write", "execute_bash", "code", "grep", "glob", "thinking"}

// OrchestratorAgentTools extends DefaultAgentTools with orchestration tools.
var OrchestratorAgentTools = slices.Concat(DefaultAgentTools, []string{"todo_list", "use_subagent", "session"})

// AvailableAgentTools is the full set of tools available to any agent.
var AvailableAgentTools = slices.Concat(OrchestratorAgentTools, []string{"introspect", "report_issue"})

// DefaultSkillsResource is the glob resource pattern for agent skills.
const DefaultSkillsResource = "skill://.kiro/skills/**/SKILL.md"

// MaxManifestBytes is the upper bound on manifest file size, shared by
// the installer (sync.go) and MCP converter (mcp.go). 1 MiB.
const MaxManifestBytes = 1 << 20

// SafeIdentifierPattern restricts values used as filenames and agent names.
var SafeIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// AgentConfig is the shared on-disk shape of .kiro/agents/*.json entries managed by kapm.
type AgentConfig struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Prompt       string   `json:"prompt"`
	Model        string   `json:"model,omitempty"`
	Tools        []string `json:"tools"`
	AllowedTools []string `json:"allowedTools"`
	Resources    []string `json:"resources,omitempty"`
}

// MarshalIndentedJSON encodes value with 2-space indent and a trailing newline.
// Callers wrap the error with their own contextual prefix.
func MarshalIndentedJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}
	return append(data, '\n'), nil
}

// ValidateIdentifier rejects empty values, path separators, "." / "..", non-canonical forms,
// and any character outside SafeIdentifierPattern. It returns the trimmed value on success.
// Errors are intentionally generic; callers wrap with their own context.
func ValidateIdentifier(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("identifier cannot be empty")
	}
	if !filepath.IsLocal(trimmed) || trimmed == "." || trimmed == ".." {
		return "", fmt.Errorf("invalid identifier %q", value)
	}
	if !SafeIdentifierPattern.MatchString(trimmed) {
		return "", fmt.Errorf("invalid identifier %q", value)
	}
	return trimmed, nil
}

// Hook event names consumed by Kiro. These are the wire-format strings that
// appear in .kiro/agents/*.json and hook-event JSON payloads.
const (
	EventAgentSpawn       = "agentSpawn"
	EventUserPromptSubmit = "userPromptSubmit"
	EventPreToolUse       = "preToolUse"
	EventPostToolUse      = "postToolUse"
	EventStop             = "stop"
)

// HookEvents is the canonical ordered list of events kapm init-hook registers.
var HookEvents = []string{
	EventAgentSpawn,
	EventUserPromptSubmit,
	EventPreToolUse,
	EventPostToolUse,
	EventStop,
}

// Tool names (wire format, matching Kiro hook payloads).
const (
	ToolRead  = "read"
	ToolWrite = "write"
	ToolShell = "shell"
	ToolGrep  = "grep"
	ToolGlob  = "glob"
)
