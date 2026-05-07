package monitor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseErrorDetail(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  []byte
		want string
	}{
		{
			name: "shell exit 1 with stderr",
			raw:  []byte(`{"items":[{"Json":{"exit_status":"exit status: 1","stderr":"command not found","stdout":""}}]}`),
			want: "exit 1: command not found",
		},
		{
			name: "shell exit 0 no detail",
			raw:  []byte(`{"items":[{"Json":{"exit_status":"exit status: 0","stderr":"","stdout":"ok"}}]}`),
			want: "",
		},
		{
			name: "shell exit 127 with stderr",
			raw:  []byte(`{"items":[{"Json":{"exit_status":"exit status: 127","stderr":"bash: foo: command not found","stdout":""}}]}`),
			want: "exit 127: bash: foo: command not found",
		},
		{
			name: "non-shell Text error",
			raw:  []byte(`{"items":[{"Text":"some error message"}]}`),
			want: "some error message",
		},
		{
			name: "nil input",
			raw:  nil,
			want: "",
		},
		{
			name: "empty items",
			raw:  []byte(`{"items":[]}`),
			want: "",
		},
		{
			name: "stderr truncated at 256",
			raw:  []byte(`{"items":[{"Json":{"exit_status":"exit status: 1","stderr":"` + strings.Repeat("x", 300) + `"}}]}`),
			want: "exit 1: " + strings.Repeat("x", 256),
		},
		{
			name: "Text truncated at 256",
			raw:  []byte(`{"items":[{"Text":"` + strings.Repeat("y", 300) + `"}]}`),
			want: strings.Repeat("y", 256),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := parseErrorDetail(json.RawMessage(c.raw))
			if got != c.want {
				t.Errorf("parseErrorDetail(%q) = %q; want %q", c.raw, got, c.want)
			}
		})
	}
}

func TestParseAssistantResponse(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  []byte
		want string
	}{
		{
			name: "JSON string unwrapped",
			raw:  []byte(`"All done. Summary here."`),
			want: "All done. Summary here.",
		},
		{
			name: "raw text fallback",
			raw:  []byte(`not a json string`),
			want: "not a json string",
		},
		{
			name: "nil",
			raw:  nil,
			want: "",
		},
		{
			name: "empty",
			raw:  []byte(""),
			want: "",
		},
		{
			name: "oversized truncated at 2048",
			raw:  []byte(`"` + strings.Repeat("z", 3000) + `"`),
			want: strings.Repeat("z", 2048),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := parseAssistantResponse(json.RawMessage(c.raw))
			if got != c.want {
				t.Errorf("parseAssistantResponse(%q) = %q; want %q", c.raw, got, c.want)
			}
		})
	}
}

func TestResolvePostToolUseWiresErrorDetail(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(30 * time.Minute)

	records := []MergedRecord{
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-fail", ToolName: "bash",
			PreToolTs: baseTime, ToolInput: []byte(`{"command":"fail"}`)},
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-fail", ToolName: "bash",
			PostToolTs: baseTime.Add(1 * time.Second), ToolStatus: ToolStatusError, ErrorDetail: "exit 1: permission denied"},
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolUse, ToolUseID: "tu-ok", ToolName: "bash",
			PreToolTs: baseTime.Add(2 * time.Second), ToolInput: []byte(`{"command":"ok"}`)},
		{SessionID: "s1", Agent: "a", Kind: RecordKindToolResult, ToolUseID: "tu-ok", ToolName: "bash",
			PostToolTs: baseTime.Add(3 * time.Second), ToolStatus: ToolStatusSuccess},
	}
	d := mustAggregate(t, records, now)
	tl := d.Sessions[0].Timeline
	// timeline[0] = preToolUse for "fail" — should have ErrorDetail set
	if tl[0].ErrorDetail != "exit 1: permission denied" {
		t.Errorf("timeline[0].ErrorDetail = %q; want %q", tl[0].ErrorDetail, "exit 1: permission denied")
	}
	// timeline[1] = preToolUse for "ok" — should have empty ErrorDetail
	if tl[1].ErrorDetail != "" {
		t.Errorf("timeline[1].ErrorDetail = %q; want empty", tl[1].ErrorDetail)
	}
}

func TestCleanSummary(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", maxSummaryLength+10)
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"all whitespace", "   \t\n\r  ", ""},
		{"multi-space", "  hello  world  ", "hello world"},
		{"tabs", "\thello\tworld\t", "hello world"},
		{"cr lf", "hello\r\nworld", "hello world"},
		{"control chars", "a\x01\x02b", "a b"},
		{"leading trailing", "  hi  ", "hi"},
		{"cjk preserved", "\u65e5\u672c  \u8a9e", "\u65e5\u672c \u8a9e"},
		{"long truncated", long, strings.Repeat("a", maxSummaryLength-len("\u2026")) + "\u2026"},
		{"ansi sequence", "\x1b[31mred\x1b[0m", "[31mred [0m"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := cleanSummary(c.in)
			if got != c.want {
				t.Errorf("cleanSummary(%q) = %q; want %q", c.in, got, c.want)
			}
		})
	}
}
