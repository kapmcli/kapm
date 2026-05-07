package monitor

// Types in this file prefixed v1 deserialize rows from the legacy kiro-cli
// SQLite conversations_v2 schema. They are unexported and used only by
// v1_convert.go (conversion to kapm's canonical session shape) and
// session_store_sqlite.go (raw scan). See the 2026-04-30-v1-sqlite-store
// plan for the migration background.

import (
	"encoding/json"
	"fmt"
)

// v1Row is one row from the v1 SQLite conversations_v2 table.
type v1Row struct {
	Key            string
	ConversationID string
	Value          string
	CreatedAt      int64 // Unix ms
	UpdatedAt      int64 // Unix ms
}

// v1Value is the JSON blob stored in the v1 SQLite value column.
// history entries exist in two formats depending on kiro-cli version:
//   - new (--no-interactive): array of objects {user, assistant, request_metadata}
//   - old (pre-migration): array of arrays [[userItem, assistantItem], ...]
type v1Value struct {
	ConversationID string           `json:"conversation_id"`
	History        []v1HistoryEntry `json:"history"`
	ModelInfo      v1ModelInfo      `json:"model_info"`
	UserTurnMeta   v1UserTurnMeta   `json:"user_turn_metadata"`
}

// v1HistoryEntry handles both history formats via custom JSON unmarshaling.
type v1HistoryEntry struct {
	User            v1UserTurn
	Assistant       v1AssistantTurn
	RequestMetadata v1RequestMeta
}

// UnmarshalJSON handles both history entry formats:
//   - object: {"user": {...}, "assistant": {...}, "request_metadata": {...}}
//   - array:  [{content, env_context, ...}, {Response/ToolUse}]
func (e *v1HistoryEntry) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '[' {
		// Old format: array of items
		var items []json.RawMessage
		if err := json.Unmarshal(b, &items); err != nil {
			return fmt.Errorf("unmarshal history array: %w", err)
		}
		// item[0] is the user message (has "content" field)
		// item[1] is the assistant message (has "Response" or "ToolUse" field)
		if len(items) > 0 {
			if err := json.Unmarshal(items[0], &e.User); err != nil {
				return fmt.Errorf("unmarshal history array user turn: %w", err)
			}
		}
		if len(items) > 1 {
			if err := json.Unmarshal(items[1], &e.Assistant); err != nil {
				return fmt.Errorf("unmarshal history array assistant turn: %w", err)
			}
		}
		return nil
	}
	// New format: object with named fields
	var obj struct {
		User            v1UserTurn      `json:"user"`
		Assistant       v1AssistantTurn `json:"assistant"`
		RequestMetadata v1RequestMeta   `json:"request_metadata"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return fmt.Errorf("unmarshal history object: %w", err)
	}
	e.User = obj.User
	e.Assistant = obj.Assistant
	e.RequestMetadata = obj.RequestMetadata
	return nil
}

type v1UserTurn struct {
	Content   v1UserContent `json:"content"`
	Timestamp string        `json:"timestamp"`
}

// v1UserContent holds exactly one of Prompt, ToolUseResults, or CancelledToolUses.
type v1UserContent struct {
	Prompt            *v1Prompt         `json:"Prompt,omitempty"`
	ToolUseResults    *v1ToolUseResults `json:"ToolUseResults,omitempty"`
	CancelledToolUses *json.RawMessage  `json:"CancelledToolUses,omitempty"`
}

type v1Prompt struct {
	Prompt string `json:"prompt"`
}

type v1ToolUseResults struct {
	// Both old and new formats use "tool_use_results".
	Results []v1ToolResult `json:"tool_use_results"`
}

// v1ToolResult content is [{Text: "..."}] in both formats.
type v1ToolResult struct {
	ToolUseID string          `json:"tool_use_id"`
	Content   []v1ToolContent `json:"content"`
	Status    string          `json:"status"`
}

type v1ToolContent struct {
	Text string          `json:"Text"`
	JSON json.RawMessage `json:"Json"`
}

// v1AssistantTurn holds exactly one of Response or ToolUse.
type v1AssistantTurn struct {
	Response *v1Response `json:"Response,omitempty"`
	ToolUse  *v1ToolUse  `json:"ToolUse,omitempty"`
}

type v1Response struct {
	MessageID string `json:"message_id"`
	Content   string `json:"content"`
}

type v1ToolUse struct {
	Thinking string       `json:"thinking"`
	ToolUses []v1ToolCall `json:"tool_uses"`
}

type v1ToolCall struct {
	Name      string          `json:"name"`
	OrigName  string          `json:"orig_name"`
	ID        string          `json:"id"`
	ToolUseID string          `json:"tool_use_id"`
	Input     json.RawMessage `json:"input"`
	Args      json.RawMessage `json:"args"`
	OrigArgs  json.RawMessage `json:"orig_args"`
}

type v1RequestMeta struct {
	RequestStartMS  int64   `json:"request_start_timestamp_ms"`
	StreamEndMS     int64   `json:"stream_end_timestamp_ms"`
	ContextUsagePct float64 `json:"context_usage_percentage"`
}

type v1ModelInfo struct {
	ModelName string `json:"model_name"`
}

type v1UserTurnMeta struct {
	UsageInfo []v1UsageInfo `json:"usage_info"`
}

type v1UsageInfo struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}
