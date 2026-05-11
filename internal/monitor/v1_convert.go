package monitor

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// convertV1Session converts a v1 SQLite row into a ParsedSession.
func convertV1Session(row v1Row) (ParsedSession, error) {
	var val v1Value
	if err := json.Unmarshal([]byte(row.Value), &val); err != nil {
		return ParsedSession{}, fmt.Errorf("v1 session unmarshal %q: %w", row.ConversationID, err)
	}

	meta := SessionMeta{
		SessionID: row.ConversationID,
		Title:     firstPrompt(val.History),
		Cwd:       row.Key,
		CreatedAt: rfc3339Time(time.UnixMilli(row.CreatedAt)),
		UpdatedAt: rfc3339Time(time.UnixMilli(row.UpdatedAt)),
		SessionState: SessionState{
			AgentName: val.ModelInfo.ModelName,
			ConversationMetadata: ConversationMetadata{
				UserTurnMetadatas: buildUserTurnMetadatas(val),
			},
		},
	}

	msgs, err := buildMessages(val.History)
	if err != nil {
		return ParsedSession{}, err
	}

	return ParsedSession{Meta: meta, Messages: msgs}, nil
}

func firstPrompt(history []v1HistoryEntry) string {
	for _, e := range history {
		if e.User.Content.Prompt != nil {
			return e.User.Content.Prompt.Prompt
		}
	}
	return ""
}

func buildUserTurnMetadatas(val v1Value) []UserTurnMetadata {
	if len(val.History) == 0 {
		return nil
	}
	turns := make([]UserTurnMetadata, len(val.History))
	for i, e := range val.History {
		rm := e.RequestMetadata
		var dur TurnDuration
		if rm.StreamEndMS > rm.RequestStartMS {
			ms := rm.StreamEndMS - rm.RequestStartMS
			dur = TurnDuration{Secs: ms / 1000, Nanos: (ms % 1000) * 1_000_000}
		}
		turns[i] = UserTurnMetadata{
			TurnDuration:    dur,
			ContextUsagePct: rm.ContextUsagePct,
		}
	}
	// Attach credits to the last turn.
	var credits []MeteringEntry
	for _, u := range val.UserTurnMeta.UsageInfo {
		if u.Unit == "credit" {
			credits = append(credits, MeteringEntry(u))
		}
	}
	if len(credits) > 0 {
		turns[len(turns)-1].MeteringUsage = credits
	}
	return turns
}

func buildMessages(history []v1HistoryEntry) ([]SessionMessage, error) {
	var msgs []SessionMessage
	for _, e := range history {
		userMsg, err := buildUserMessage(e)
		if err != nil {
			return nil, err
		}
		if userMsg != nil {
			msgs = append(msgs, *userMsg)
		}

		assistantMsg, err := buildAssistantMessage(e)
		if err != nil {
			return nil, err
		}
		if assistantMsg != nil {
			msgs = append(msgs, *assistantMsg)
		}
	}
	return msgs, nil
}

func buildUserMessage(e v1HistoryEntry) (*SessionMessage, error) {
	c := e.User.Content
	switch {
	case c.Prompt != nil:
		data, err := buildPromptContent(e)
		if err != nil {
			return nil, err
		}
		return &SessionMessage{Kind: MessageKindPrompt, Data: data}, nil
	case c.ToolUseResults != nil:
		data, err := buildToolResultContent(e)
		if err != nil {
			return nil, err
		}
		return &SessionMessage{Kind: MessageKindToolResults, Data: data}, nil
	default:
		// CancelledToolUses or unknown — skip
		return nil, nil
	}
}

func buildPromptContent(e v1HistoryEntry) (json.RawMessage, error) {
	textData, err := json.Marshal(e.User.Content.Prompt.Prompt)
	if err != nil {
		return nil, err
	}
	pd := PromptData{
		Content: []ContentItem{{Kind: "text", Data: json.RawMessage(textData)}},
		Meta:    PromptMeta{Timestamp: parseTimestamp(e.User.Timestamp)},
	}
	return json.Marshal(pd)
}

func buildToolResultContent(e v1HistoryEntry) (json.RawMessage, error) {
	items := make([]ContentItem, len(e.User.Content.ToolUseResults.Results))
	for i, r := range e.User.Content.ToolUseResults.Results {
		status := ToolStatusSuccess
		if r.Status == "Error" || r.Status == "error" {
			status = ToolStatusError
		}
		var resultContent []ContentItem
		var sb strings.Builder
		for _, tc := range r.Content {
			if tc.Text != "" {
				sb.WriteString(tc.Text)
			}
			if len(tc.JSON) > 0 && string(tc.JSON) != "null" {
				resultContent = append(resultContent, ContentItem{Kind: ContentKindJSON, Data: tc.JSON})
			}
		}
		text := sb.String()
		if text != "" || len(resultContent) == 0 {
			textData, err := json.Marshal(text)
			if err != nil {
				return nil, err
			}
			resultContent = append([]ContentItem{{Kind: ContentKindText, Data: json.RawMessage(textData)}}, resultContent...)
		}
		trd := ToolResultData{ToolUseID: r.ToolUseID, Content: resultContent, Status: status}
		d, err := json.Marshal(trd)
		if err != nil {
			return nil, err
		}
		items[i] = ContentItem{Kind: "toolResult", Data: d}
	}
	return json.Marshal(struct {
		Content []ContentItem `json:"content"`
	}{Content: items})
}

func buildAssistantMessage(e v1HistoryEntry) (*SessionMessage, error) {
	a := e.Assistant
	switch {
	case a.Response != nil:
		textData, err := json.Marshal(a.Response.Content)
		if err != nil {
			return nil, err
		}
		ad := AssistantData{
			MessageID: a.Response.MessageID,
			Content:   []ContentItem{{Kind: "text", Data: json.RawMessage(textData)}},
		}
		data, err := json.Marshal(ad)
		if err != nil {
			return nil, err
		}
		return &SessionMessage{Kind: MessageKindAssistantMessage, Data: data}, nil

	case a.ToolUse != nil:
		items := make([]ContentItem, len(a.ToolUse.ToolUses))
		for i, tc := range a.ToolUse.ToolUses {
			tud := ToolUseData{
				ToolUseID: firstNonEmpty(tc.ToolUseID, tc.ID),
				Name:      firstNonEmpty(tc.Name, tc.OrigName),
				Input:     firstNonEmptyRaw(tc.Input, tc.Args, tc.OrigArgs),
			}
			d, err := json.Marshal(tud)
			if err != nil {
				return nil, err
			}
			items[i] = ContentItem{Kind: "toolUse", Data: d}
		}
		ad := AssistantData{Content: items}
		data, err := json.Marshal(ad)
		if err != nil {
			return nil, err
		}
		return &SessionMessage{Kind: MessageKindAssistantMessage, Data: data}, nil

	default:
		return nil, nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmptyRaw(values ...json.RawMessage) json.RawMessage {
	for _, v := range values {
		if len(v) > 0 && string(v) != "null" {
			return v
		}
	}
	return nil
}

// parseTimestamp converts an RFC3339 timestamp string to unix seconds.
// Returns 0 if the string is empty or unparseable.
func parseTimestamp(s string) int64 {
	if s == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return 0
		}
	}
	return t.Unix()
}
