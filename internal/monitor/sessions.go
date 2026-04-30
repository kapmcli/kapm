package monitor

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"time"
)

// SessionMessage is one line of a sessions .jsonl file.
type SessionMessage struct {
	Version string          `json:"version"`
	Kind    string          `json:"kind"` // "Prompt", "AssistantMessage", "ToolResults"
	Data    json.RawMessage `json:"data"`
}

// PromptData is the data field when Kind=="Prompt".
type PromptData struct {
	MessageID string        `json:"message_id"`
	Content   []ContentItem `json:"content"`
	Meta      PromptMeta    `json:"meta"`
}

// PromptMeta holds per-message metadata for a Prompt.
type PromptMeta struct {
	Timestamp         int64  `json:"timestamp"` // unix seconds
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// AssistantData is the data field when Kind=="AssistantMessage".
type AssistantData struct {
	MessageID string        `json:"message_id"`
	Content   []ContentItem `json:"content"`
}

// ContentItem is one element of a content array.
type ContentItem struct {
	Kind string          `json:"kind"` // "text", "toolUse", "toolResult"
	Data json.RawMessage `json:"data"`
}

// ToolUseData is ContentItem.Data when Kind=="toolUse".
type ToolUseData struct {
	ToolUseID string          `json:"toolUseId"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

// ToolResultData is ContentItem.Data when Kind=="toolResult".
type ToolResultData struct {
	ToolUseID string        `json:"toolUseId"`
	Content   []ContentItem `json:"content"`
	Status    string        `json:"status"` // "success" or "error"
}

// rfc3339Time is a time.Time that JSON-unmarshals from an RFC3339 string.
// An empty string unmarshals to the zero time (no error).
// A malformed non-empty string returns an error.
type rfc3339Time time.Time

func (t rfc3339Time) MarshalJSON() ([]byte, error) {
	tt := time.Time(t)
	if tt.IsZero() {
		return []byte(`""`), nil
	}
	return []byte(`"` + tt.UTC().Format(time.RFC3339) + `"`), nil
}

func (t *rfc3339Time) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		*t = rfc3339Time(time.Time{})
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return err
	}
	*t = rfc3339Time(parsed)
	return nil
}

// SessionMeta is the top-level structure of a {uuid}.json file.
type SessionMeta struct {
	SessionID    string       `json:"session_id"`
	Title        string       `json:"title"`
	Cwd          string       `json:"cwd"`
	CreatedAt    rfc3339Time  `json:"created_at"`
	UpdatedAt    rfc3339Time  `json:"updated_at"`
	SessionState SessionState `json:"session_state"`
}

// SessionState holds agent and conversation metadata.
type SessionState struct {
	AgentName            string               `json:"agent_name"`
	ConversationMetadata ConversationMetadata `json:"conversation_metadata"`
}

// ConversationMetadata holds per-turn metadata.
type ConversationMetadata struct {
	UserTurnMetadatas []UserTurnMetadata `json:"user_turn_metadatas"`
}

// UserTurnMetadata holds metrics for one user turn.
type UserTurnMetadata struct {
	TurnDuration     TurnDuration    `json:"turn_duration"`
	EndTimestamp     string          `json:"end_timestamp,omitempty"`
	EndReason        string          `json:"end_reason,omitempty"`
	InputTokenCount  int             `json:"input_token_count"`
	OutputTokenCount int             `json:"output_token_count"`
	ContextUsagePct  float64         `json:"context_usage_percentage"`
	MeteringUsage    []MeteringEntry `json:"metering_usage"`
}

// TurnDuration is a duration split into seconds and nanoseconds.
type TurnDuration struct {
	Secs  int64 `json:"secs"`
	Nanos int64 `json:"nanos"`
}

// MeteringEntry is one metering value with a unit.
type MeteringEntry struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

// ParseSessionJSONL reads a sessions .jsonl file and returns the parsed
// messages. Unknown kind values and malformed lines are silently skipped;
// skipped counts the number of skipped lines.
func ParseSessionJSONL(r io.Reader) ([]SessionMessage, int, error) {
	var msgs []SessionMessage
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024) // 10 MB max line
	var skipped int
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg SessionMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			skipped++
			continue // skip incomplete/malformed lines
		}
		switch msg.Kind {
		case "Prompt", "AssistantMessage", "ToolResults":
			msgs = append(msgs, msg)
		default:
			// unknown kind — silently skip for forward compatibility
		}
	}
	if skipped > 0 {
		slog.Warn("skipped malformed session lines", "count", skipped)
	}
	if err := scanner.Err(); err != nil {
		return nil, skipped, err
	}
	return msgs, skipped, nil
}

// ParseSessionMeta reads a {uuid}.json file and returns the parsed SessionMeta.
func ParseSessionMeta(r io.Reader) (SessionMeta, error) {
	var meta SessionMeta
	if err := json.NewDecoder(r).Decode(&meta); err != nil {
		return SessionMeta{}, err
	}
	return meta, nil
}
