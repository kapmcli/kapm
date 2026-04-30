package monitor

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestParseSessionJSONL(t *testing.T) {
	t.Run("valid fixture", func(t *testing.T) {
		f, err := os.Open("testdata/sessions/valid.jsonl")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()

		msgs, skipped, err := ParseSessionJSONL(f)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if skipped != 0 {
			t.Errorf("skipped=%d, want 0", skipped)
		}
		if len(msgs) != 3 {
			t.Fatalf("len(msgs)=%d, want 3", len(msgs))
		}
		if msgs[0].Kind != MessageKindPrompt {
			t.Errorf("msgs[0].Kind=%q, want Prompt", msgs[0].Kind)
		}
		if msgs[1].Kind != MessageKindAssistantMessage {
			t.Errorf("msgs[1].Kind=%q, want AssistantMessage", msgs[1].Kind)
		}
		if msgs[2].Kind != MessageKindToolResults {
			t.Errorf("msgs[2].Kind=%q, want ToolResults", msgs[2].Kind)
		}
	})

	t.Run("prompt data fields", func(t *testing.T) {
		input := `{"version":"v1","kind":"Prompt","data":{"message_id":"id1","content":[{"kind":"text","data":"hi"}],"meta":{"timestamp":1700000000}}}` + "\n"
		msgs, _, err := ParseSessionJSONL(strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("len=%d, want 1", len(msgs))
		}
		var pd PromptData
		if err := json.Unmarshal(msgs[0].Data, &pd); err != nil {
			t.Fatalf("unmarshal PromptData: %v", err)
		}
		if pd.MessageID != "id1" {
			t.Errorf("MessageID=%q, want id1", pd.MessageID)
		}
		if pd.Meta.Timestamp != 1700000000 {
			t.Errorf("Timestamp=%d, want 1700000000", pd.Meta.Timestamp)
		}
		if len(pd.Content) != 1 || pd.Content[0].Kind != "text" {
			t.Errorf("unexpected content: %+v", pd.Content)
		}
	})

	t.Run("assistant message with toolUse", func(t *testing.T) {
		input := `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"id2","content":[{"kind":"toolUse","data":{"toolUseId":"tu1","name":"shell","input":{}}}]}}` + "\n"
		msgs, _, err := ParseSessionJSONL(strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		var ad AssistantData
		if err := json.Unmarshal(msgs[0].Data, &ad); err != nil {
			t.Fatalf("unmarshal AssistantData: %v", err)
		}
		if len(ad.Content) != 1 {
			t.Fatalf("len(content)=%d, want 1", len(ad.Content))
		}
		var tu ToolUseData
		if err := json.Unmarshal(ad.Content[0].Data, &tu); err != nil {
			t.Fatalf("unmarshal ToolUseData: %v", err)
		}
		if tu.ToolUseID != "tu1" {
			t.Errorf("ToolUseID=%q, want tu1", tu.ToolUseID)
		}
		if tu.Name != "shell" {
			t.Errorf("Name=%q, want shell", tu.Name)
		}
	})

	t.Run("tool results success and error", func(t *testing.T) {
		input := `{"version":"v1","kind":"ToolResults","data":{"message_id":"id3","content":[{"kind":"toolResult","data":{"toolUseId":"tu1","content":[],"status":"success"}},{"kind":"toolResult","data":{"toolUseId":"tu2","content":[],"status":"error"}}]}}` + "\n"
		msgs, _, err := ParseSessionJSONL(strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		// ToolResults data has content array with toolResult items
		var raw struct {
			Content []ContentItem `json:"content"`
		}
		if err := json.Unmarshal(msgs[0].Data, &raw); err != nil {
			t.Fatal(err)
		}
		if len(raw.Content) != 2 {
			t.Fatalf("len(content)=%d, want 2", len(raw.Content))
		}
		var tr1 ToolResultData
		if err := json.Unmarshal(raw.Content[0].Data, &tr1); err != nil {
			t.Fatal(err)
		}
		if tr1.Status != ToolStatusSuccess {
			t.Errorf("status=%q, want success", tr1.Status)
		}
		var tr2 ToolResultData
		if err := json.Unmarshal(raw.Content[1].Data, &tr2); err != nil {
			t.Fatal(err)
		}
		if tr2.Status != ToolStatusError {
			t.Errorf("status=%q, want error", tr2.Status)
		}
	})
}

func TestParseSessionJSONL_Malformed(t *testing.T) {
	t.Run("malformed fixture", func(t *testing.T) {
		f, err := os.Open("testdata/sessions/malformed.jsonl")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()

		msgs, skipped, err := ParseSessionJSONL(f)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// 2 malformed lines ({not valid json, {partial line)
		if skipped != 2 {
			t.Errorf("skipped=%d, want 2", skipped)
		}
		// good Prompt + good AssistantMessage = 2 (UnknownKind silently skipped)
		if len(msgs) != 2 {
			t.Errorf("len(msgs)=%d, want 2", len(msgs))
		}
	})

	t.Run("unknown kind silently skipped", func(t *testing.T) {
		input := `{"version":"v1","kind":"FutureKind","data":{}}` + "\n" +
			`{"version":"v1","kind":"Prompt","data":{"message_id":"x","content":[],"meta":{"timestamp":0}}}` + "\n"
		msgs, skipped, err := ParseSessionJSONL(strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		if skipped != 0 {
			t.Errorf("skipped=%d, want 0 (unknown kind is not a parse error)", skipped)
		}
		if len(msgs) != 1 {
			t.Errorf("len(msgs)=%d, want 1", len(msgs))
		}
		if msgs[0].Kind != MessageKindPrompt {
			t.Errorf("kind=%q, want Prompt", msgs[0].Kind)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		msgs, skipped, err := ParseSessionJSONL(strings.NewReader(""))
		if err != nil {
			t.Fatal(err)
		}
		if skipped != 0 {
			t.Errorf("skipped=%d, want 0", skipped)
		}
		if len(msgs) != 0 {
			t.Errorf("len(msgs)=%d, want 0", len(msgs))
		}
	})

	t.Run("partial line only", func(t *testing.T) {
		msgs, skipped, err := ParseSessionJSONL(strings.NewReader(`{"version":"v1","kind":"Prompt"`))
		if err != nil {
			t.Fatal(err)
		}
		if skipped != 1 {
			t.Errorf("skipped=%d, want 1", skipped)
		}
		if len(msgs) != 0 {
			t.Errorf("len(msgs)=%d, want 0", len(msgs))
		}
	})

	t.Run("invalid json line", func(t *testing.T) {
		input := "not json at all\n" +
			`{"version":"v1","kind":"Prompt","data":{"message_id":"y","content":[],"meta":{"timestamp":1}}}` + "\n"
		msgs, skipped, err := ParseSessionJSONL(strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		if skipped != 1 {
			t.Errorf("skipped=%d, want 1", skipped)
		}
		if len(msgs) != 1 {
			t.Errorf("len(msgs)=%d, want 1", len(msgs))
		}
	})
}

func TestParseSessionMeta(t *testing.T) {
	t.Run("full meta fixture", func(t *testing.T) {
		f, err := os.Open("testdata/sessions/meta.json")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()

		meta, err := ParseSessionMeta(f)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.SessionID != "cccc0000-0000-0000-0000-000000000001" {
			t.Errorf("SessionID=%q", meta.SessionID)
		}
		if meta.Title != "test session" {
			t.Errorf("Title=%q", meta.Title)
		}
		if meta.Cwd != "/home/user/project" {
			t.Errorf("Cwd=%q", meta.Cwd)
		}
		if meta.SessionState.AgentName != "lead" {
			t.Errorf("AgentName=%q", meta.SessionState.AgentName)
		}
		turns := meta.SessionState.ConversationMetadata.UserTurnMetadatas
		if len(turns) != 1 {
			t.Fatalf("len(turns)=%d, want 1", len(turns))
		}
		if turns[0].InputTokenCount != 100 {
			t.Errorf("InputTokenCount=%d, want 100", turns[0].InputTokenCount)
		}
		if turns[0].TurnDuration.Secs != 10 {
			t.Errorf("TurnDuration.Secs=%d, want 10", turns[0].TurnDuration.Secs)
		}
		if len(turns[0].MeteringUsage) != 1 {
			t.Errorf("len(MeteringUsage)=%d, want 1", len(turns[0].MeteringUsage))
		}
	})

	t.Run("minimal meta fixture", func(t *testing.T) {
		f, err := os.Open("testdata/sessions/meta_minimal.json")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()

		meta, err := ParseSessionMeta(f)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.SessionID != "dddd0000-0000-0000-0000-000000000001" {
			t.Errorf("SessionID=%q", meta.SessionID)
		}
		if meta.Title != "" {
			t.Errorf("Title=%q, want empty", meta.Title)
		}
		if len(meta.SessionState.ConversationMetadata.UserTurnMetadatas) != 0 {
			t.Errorf("expected empty turns")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := ParseSessionMeta(strings.NewReader("not json"))
		if err == nil {
			t.Error("expected error for invalid json")
		}
	})
}
