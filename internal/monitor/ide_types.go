package monitor

import (
	"encoding/json"
	"time"
)

// IDE action type constants (wire format values from IDE execution logs).
const (
	ActionReadFiles        = "readFiles"
	ActionRunCommand       = "runCommand"
	ActionWrite            = "write"
	ActionCreate           = "create"
	ActionDelete           = "delete"
	ActionSearch           = "search"
	ActionInvokeSubAgent   = "invokeSubAgent"
	ActionSubAgentResponse = "subagentResponse"

	// ToolNameShell is the display name for ActionRunCommand.
	ToolNameShell = "shell"
)

// Session-identifier naming across IDE and Kiro schemas
//
// The "session identifier" concept appears under three distinct JSON field names
// because the IDE format is an external schema not under kapm's control:
//
//   - [IDESessionEntry].SessionID     → JSON "sessionId"      (external IDE schema)
//   - [IDEExecutionLog].ChatSessionID → JSON "chatSessionId"  (external IDE schema)
//   - SessionMeta.SessionID           → JSON "session_id"     (internal Kiro schema, defined in types.go)
//
// The conversion boundary is in ide_sessions.go and v1_convert.go.

// IDESessionEntry is one entry in workspace-sessions/{base64}/sessions.json.
type IDESessionEntry struct {
	SessionID          string `json:"sessionId"`
	Title              string `json:"title"`
	DateCreated        string `json:"dateCreated"` // unix ms as string
	WorkspaceDirectory string `json:"workspaceDirectory"`
}

// IDESessionHistory is the top-level structure of {sessionId}.json.
type IDESessionHistory struct {
	History []IDEHistoryEntry `json:"history"`
}

// IDEHistoryEntry is one element of the history array.
type IDEHistoryEntry struct {
	Message     IDEMessage `json:"message"`
	ExecutionID string     `json:"executionId,omitempty"` // assistant entries only
}

// IDEMessage is a conversation message.
type IDEMessage struct {
	Role    string          `json:"role"`    // "user" | "assistant"
	Content json.RawMessage `json:"content"` // string or []ContentItem
	ID      string          `json:"id"`
}

// IDEExecutionIndex is the top-level structure of {profileHash}/{indexHash}.
type IDEExecutionIndex struct {
	Executions []IDEExecutionEntry `json:"executions"`
}

// IDEExecutionEntry is one execution in the index.
type IDEExecutionEntry struct {
	ExecutionID string `json:"executionId"`
	Type        string `json:"type"`      // "chat-agent"
	Status      string `json:"status"`    // "succeed" | "aborted"
	StartTime   int64  `json:"startTime"` // unix ms
	EndTime     int64  `json:"endTime"`   // unix ms
}

// IDEExecutionLog is the top-level structure of {profileHash}/{sessionHash}/{executionHash}.
type IDEExecutionLog struct {
	ExecutionID            string          `json:"executionId"`
	WorkflowType           string          `json:"workflowType"`
	Status                 string          `json:"status"`
	StartTime              int64           `json:"startTime"`
	EndTime                int64           `json:"endTime"`
	ChatSessionID          string          `json:"chatSessionId"`
	Actions                []IDEAction     `json:"actions"`
	UsageSummary           []IDEUsageEntry `json:"usageSummary"`
	ContextUsagePercentage float64         `json:"contextUsagePercentage"`
}

// IDEAction is one entry in the actions array of an execution log.
type IDEAction struct {
	ActionType        string          `json:"actionType"`
	ActionID          string          `json:"actionId"`
	ActionState       string          `json:"actionState"` // Success, Accepted, Rejected, Error
	EmittedAt         int64           `json:"emittedAt"`   // unix ms
	EndTime           int64           `json:"endTime"`     // unix ms, optional
	ErrorMessage      string          `json:"errorMessage"`
	Input             json.RawMessage `json:"input"`
	Output            json.RawMessage `json:"output"`
	EstimatedDuration time.Duration   // computed from gap to next action
}

// IDEUsageEntry is one entry in usageSummary.
type IDEUsageEntry struct {
	Unit      string   `json:"unit"` // "credit"
	Usage     float64  `json:"usage"`
	UsedTools []string `json:"usedTools,omitempty"`
}
