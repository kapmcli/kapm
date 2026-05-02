package monitor

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestConvertV1Session_SimplePrompt(t *testing.T) {
	row := v1Row{
		Key:            "/home/user/project",
		ConversationID: "conv-123",
		Value: `{
			"conversation_id": "conv-123",
			"history": [{
				"user": {
					"content": {"Prompt": {"prompt": "hello world"}},
					"timestamp": "2024-01-01T10:00:00Z"
				},
				"assistant": {
					"Response": {"message_id": "msg-1", "content": "hi there"}
				},
				"request_metadata": {
					"request_start_timestamp_ms": 1704103200000,
					"stream_end_timestamp_ms": 1704103204000,
					"context_usage_percentage": 1.5
				}
			}],
			"model_info": {"model_name": "claude-opus-4"},
			"user_turn_metadata": {"usage_info": []}
		}`,
		CreatedAt: 1704103200000,
		UpdatedAt: 1704103204000,
	}

	ps, err := convertV1Session(row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// SessionMeta
	if ps.Meta.SessionID != "conv-123" {
		t.Errorf("SessionID = %q, want %q", ps.Meta.SessionID, "conv-123")
	}
	if ps.Meta.Title != "hello world" {
		t.Errorf("Title = %q, want %q", ps.Meta.Title, "hello world")
	}
	if ps.Meta.Cwd != "/home/user/project" {
		t.Errorf("Cwd = %q, want %q", ps.Meta.Cwd, "/home/user/project")
	}
	wantCreated := time.UnixMilli(1704103200000).UTC()
	if time.Time(ps.Meta.CreatedAt).UTC() != wantCreated {
		t.Errorf("CreatedAt = %v, want %v", time.Time(ps.Meta.CreatedAt).UTC(), wantCreated)
	}
	if ps.Meta.SessionState.AgentName != "claude-opus-4" {
		t.Errorf("AgentName = %q, want %q", ps.Meta.SessionState.AgentName, "claude-opus-4")
	}

	// Messages: [Prompt, AssistantMessage]
	if len(ps.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(ps.Messages))
	}
	if ps.Messages[0].Kind != MessageKindPrompt {
		t.Errorf("Messages[0].Kind = %q, want %q", ps.Messages[0].Kind, MessageKindPrompt)
	}
	if ps.Messages[1].Kind != MessageKindAssistantMessage {
		t.Errorf("Messages[1].Kind = %q, want %q", ps.Messages[1].Kind, MessageKindAssistantMessage)
	}

	var pd PromptData
	if err := json.Unmarshal(ps.Messages[0].Data, &pd); err != nil {
		t.Fatalf("unmarshal PromptData: %v", err)
	}
	if len(pd.Content) != 1 || pd.Content[0].Kind != "text" {
		t.Errorf("PromptData.Content = %v, want [{text ...}]", pd.Content)
	}

	var ad AssistantData
	if err := json.Unmarshal(ps.Messages[1].Data, &ad); err != nil {
		t.Fatalf("unmarshal AssistantData: %v", err)
	}
	if len(ad.Content) != 1 || ad.Content[0].Kind != "text" {
		t.Errorf("AssistantData.Content = %v, want [{text ...}]", ad.Content)
	}
}

func TestConvertV1Session_WithToolUse(t *testing.T) {
	row := v1Row{
		Key:            "/proj",
		ConversationID: "conv-tool",
		Value: `{
			"conversation_id": "conv-tool",
			"history": [
				{
					"user": {
						"content": {"Prompt": {"prompt": "use a tool"}},
						"timestamp": "2024-01-01T10:00:00Z"
					},
					"assistant": {
						"ToolUse": {
							"thinking": "I should use a tool",
							"tool_uses": [{"name": "bash", "tool_use_id": "tu-1", "input": {"command": "ls"}}]
						}
					},
					"request_metadata": {"context_usage_percentage": 2.0}
				},
				{
					"user": {
						"content": {"ToolUseResults": {"tool_use_results": [{"tool_use_id": "tu-1", "content": [{"Text": "file.txt"}], "status": "Success"}]}},
						"timestamp": "2024-01-01T10:00:05Z"
					},
					"assistant": {
						"Response": {"message_id": "msg-2", "content": "done"}
					},
					"request_metadata": {"context_usage_percentage": 3.0}
				}
			],
			"model_info": {"model_name": "claude-opus-4"},
			"user_turn_metadata": {"usage_info": []}
		}`,
		CreatedAt: 1704103200000,
		UpdatedAt: 1704103210000,
	}

	ps, err := convertV1Session(row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect: Prompt, AssistantMessage(toolUse), ToolResults, AssistantMessage(text)
	if len(ps.Messages) != 4 {
		t.Fatalf("len(Messages) = %d, want 4", len(ps.Messages))
	}
	if ps.Messages[0].Kind != MessageKindPrompt {
		t.Errorf("Messages[0].Kind = %q", ps.Messages[0].Kind)
	}
	if ps.Messages[1].Kind != MessageKindAssistantMessage {
		t.Errorf("Messages[1].Kind = %q", ps.Messages[1].Kind)
	}
	// AssistantMessage from ToolUse should have toolUse ContentItem
	var ad AssistantData
	if err := json.Unmarshal(ps.Messages[1].Data, &ad); err != nil {
		t.Fatalf("unmarshal AssistantData: %v", err)
	}
	if len(ad.Content) != 1 || ad.Content[0].Kind != "toolUse" {
		t.Errorf("AssistantData.Content = %v, want [{toolUse ...}]", ad.Content)
	}
	if ps.Messages[2].Kind != MessageKindToolResults {
		t.Errorf("Messages[2].Kind = %q", ps.Messages[2].Kind)
	}
	var trs struct {
		Content []ContentItem `json:"content"`
	}
	if err := json.Unmarshal(ps.Messages[2].Data, &trs); err != nil {
		t.Fatalf("unmarshal ToolResults data: %v", err)
	}
	if len(trs.Content) != 1 || trs.Content[0].Kind != ContentKindToolResult {
		t.Fatalf("ToolResults.Content = %+v, want one toolResult", trs.Content)
	}
	recs := MergeSessions([]ParsedSession{ps}, nil)
	var foundToolResult bool
	for _, rec := range recs {
		if rec.Kind == RecordKindToolResult && rec.ToolUseID == "tu-1" && rec.ToolStatus == ToolStatusSuccess {
			foundToolResult = true
		}
	}
	if !foundToolResult {
		t.Fatalf("merged records missing successful toolResult for tu-1: %+v", recs)
	}
	if ps.Messages[3].Kind != MessageKindAssistantMessage {
		t.Errorf("Messages[3].Kind = %q", ps.Messages[3].Kind)
	}
}

func TestConvertV1Session_WithNoInteractiveToolUseFields(t *testing.T) {
	row := v1Row{
		Key:            "/proj",
		ConversationID: "conv-no-interactive-tool",
		Value: `{
			"conversation_id": "conv-no-interactive-tool",
			"history": [
				{
					"user": {
						"content": {"Prompt": {"prompt": "delegate"}},
						"timestamp": "2024-01-01T10:00:00Z"
					},
					"assistant": {
						"ToolUse": {
							"tool_uses": [{"name": "use_subagent", "id": "tooluse_1", "args": {"command": "InvokeSubagents", "content": {"subagents": [{"agent_name": "explorer", "query": "inspect the repo", "relevant_context": "cwd=/proj"}]}}}]
						}
					},
					"request_metadata": {"context_usage_percentage": 2.0}
				},
				{
					"user": {
						"content": {"ToolUseResults": {"tool_use_results": [{"tool_use_id": "tooluse_1", "content": [{"Json": {"summaries": [{"taskDescription": "Inspect code", "contextSummary": "Read files", "taskResult": "Found monitor package"}]}}], "status": "Success"}]}},
						"timestamp": "2024-01-01T10:00:05Z"
					},
					"assistant": {
						"Response": {"message_id": "msg-2", "content": "ok"}
					},
					"request_metadata": {"context_usage_percentage": 3.0}
				}
			],
			"model_info": {"model_name": "auto"},
			"user_turn_metadata": {"usage_info": []}
		}`,
		CreatedAt: 1704103200000,
		UpdatedAt: 1704103210000,
	}

	ps, err := convertV1Session(row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var ad AssistantData
	if err := json.Unmarshal(ps.Messages[1].Data, &ad); err != nil {
		t.Fatalf("unmarshal AssistantData: %v", err)
	}
	var tu ToolUseData
	if err := json.Unmarshal(ad.Content[0].Data, &tu); err != nil {
		t.Fatalf("unmarshal ToolUseData: %v", err)
	}
	if tu.ToolUseID != "tooluse_1" {
		t.Fatalf("ToolUseID = %q, want tooluse_1", tu.ToolUseID)
	}
	if tu.Name != "use_subagent" {
		t.Fatalf("Name = %q, want use_subagent", tu.Name)
	}
	if string(tu.Input) != `{"command":"InvokeSubagents","content":{"subagents":[{"agent_name":"explorer","query":"inspect the repo","relevant_context":"cwd=/proj"}]}}` {
		t.Fatalf("Input = %s, want args JSON", tu.Input)
	}

	recs := MergeSessions([]ParsedSession{ps}, nil)
	var foundSuccess bool
	for _, rec := range recs {
		if rec.Kind == RecordKindToolResult && rec.ToolUseID == "tooluse_1" && rec.ToolStatus == ToolStatusSuccess {
			foundSuccess = true
		}
	}
	if !foundSuccess {
		t.Fatalf("merged records missing successful toolResult for tooluse_1: %+v", recs)
	}
	detail, err := AggregateDetail(context.Background(), recs, time.UnixMilli(1704103210000))
	if err != nil {
		t.Fatalf("AggregateDetail: %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("len(Sessions) = %d, want 1", len(detail.Sessions))
	}
	if len(detail.Sessions[0].SubAgentCalls) != 1 {
		t.Fatalf("len(SubAgentCalls) = %d, want 1", len(detail.Sessions[0].SubAgentCalls))
	}
	sa := detail.Sessions[0].SubAgentCalls[0]
	if sa.AgentName != "explorer" {
		t.Fatalf("AgentName = %q, want explorer", sa.AgentName)
	}
	if sa.Prompt != "inspect the repo" {
		t.Fatalf("Prompt = %q, want inspect the repo", sa.Prompt)
	}
	if sa.Explanation != "Inspect code" {
		t.Fatalf("Explanation = %q, want Inspect code", sa.Explanation)
	}
	if sa.Response != "Found monitor package" {
		t.Fatalf("Response = %q, want Found monitor package", sa.Response)
	}
}

func TestConvertV1Session_Credits(t *testing.T) {
	row := v1Row{
		Key:            "/proj",
		ConversationID: "conv-credits",
		Value: `{
			"conversation_id": "conv-credits",
			"history": [{
				"user": {
					"content": {"Prompt": {"prompt": "hi"}},
					"timestamp": "2024-01-01T10:00:00Z"
				},
				"assistant": {
					"Response": {"message_id": "msg-1", "content": "hello"}
				},
				"request_metadata": {"context_usage_percentage": 1.0}
			}],
			"model_info": {"model_name": "claude-opus-4"},
			"user_turn_metadata": {
				"usage_info": [
					{"value": 0.148, "unit": "credit"},
					{"value": 5, "unit": "token"}
				]
			}
		}`,
		CreatedAt: 1704103200000,
		UpdatedAt: 1704103204000,
	}

	ps, err := convertV1Session(row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	meta := ps.Meta.SessionState.ConversationMetadata
	if len(meta.UserTurnMetadatas) == 0 {
		t.Fatal("UserTurnMetadatas is empty")
	}
	// Credits should be in the last turn's MeteringUsage
	lastTurn := meta.UserTurnMetadatas[len(meta.UserTurnMetadatas)-1]
	var found bool
	for _, m := range lastTurn.MeteringUsage {
		if m.Unit == "credit" && m.Value == 0.148 {
			found = true
		}
	}
	if !found {
		t.Errorf("credit MeteringEntry not found in last turn: %+v", lastTurn.MeteringUsage)
	}
	// Token counts should be 0
	if lastTurn.InputTokenCount != 0 || lastTurn.OutputTokenCount != 0 {
		t.Errorf("token counts should be 0, got input=%d output=%d", lastTurn.InputTokenCount, lastTurn.OutputTokenCount)
	}
}

func TestConvertV1Session_OldArrayFormat(t *testing.T) {
	// Old format: history is array of arrays [[userItem, assistantItem], ...]
	row := v1Row{
		Key:            "/proj",
		ConversationID: "conv-old",
		Value: `{
			"conversation_id": "conv-old",
			"history": [
				[
					{"additional_context": "", "env_context": {"env_state": {}}, "content": {"Prompt": {"prompt": "old hello"}}, "images": null},
					{"Response": {"message_id": "msg-old", "content": "old reply"}}
				]
			],
			"model_info": {"model_name": "claude-old"}
		}`,
		CreatedAt: 1704103200000,
		UpdatedAt: 1704103204000,
	}

	ps, err := convertV1Session(row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ps.Meta.Title != "old hello" {
		t.Errorf("Title = %q, want %q", ps.Meta.Title, "old hello")
	}
	if len(ps.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(ps.Messages))
	}
	if ps.Messages[0].Kind != MessageKindPrompt {
		t.Errorf("Messages[0].Kind = %q", ps.Messages[0].Kind)
	}
	if ps.Messages[1].Kind != MessageKindAssistantMessage {
		t.Errorf("Messages[1].Kind = %q", ps.Messages[1].Kind)
	}
}

func TestConvertV1Session_EmptyHistory(t *testing.T) {
	row := v1Row{
		Key:            "/proj",
		ConversationID: "conv-empty",
		Value: `{
			"conversation_id": "conv-empty",
			"history": [],
			"model_info": {"model_name": "claude-opus-4"},
			"user_turn_metadata": {"usage_info": []}
		}`,
		CreatedAt: 1704103200000,
		UpdatedAt: 1704103200000,
	}

	ps, err := convertV1Session(row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ps.Meta.Title != "" {
		t.Errorf("Title = %q, want empty", ps.Meta.Title)
	}
	if len(ps.Messages) != 0 {
		t.Errorf("len(Messages) = %d, want 0", len(ps.Messages))
	}
}
