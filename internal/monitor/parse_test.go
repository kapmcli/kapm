package monitor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

func TestParseErrorDetail(t *testing.T) {
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
			got := parseErrorDetail(json.RawMessage(c.raw))
			if got != c.want {
				t.Errorf("parseErrorDetail(%q) = %q; want %q", c.raw, got, c.want)
			}
		})
	}
}

func TestParseAssistantResponse(t *testing.T) {
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
			got := parseAssistantResponse(json.RawMessage(c.raw))
			if got != c.want {
				t.Errorf("parseAssistantResponse(%q) = %q; want %q", c.raw, got, c.want)
			}
		})
	}
}

func TestResolvePostToolUseWiresErrorDetail(t *testing.T) {
	now := baseTime.Add(30 * time.Minute)
	exitStatus1WithStderr := json.RawMessage(`{"items":[{"Json":{"exit_status":"exit status: 1","stderr":"permission denied","stdout":""}}]}`)
	exitStatus0 := json.RawMessage(`{"items":[{"Json":{"exit_status":"exit status: 0"}}]}`)

	records := []Record{
		{Ts: baseTime, Session: "s1", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"fail"}`)},
		{Ts: baseTime.Add(1 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"fail"}`), ToolResponse: exitStatus1WithStderr},
		{Ts: baseTime.Add(2 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPreToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"ok"}`)},
		{Ts: baseTime.Add(3 * time.Second), Session: "s1", Agent: "a", Event: apmconfig.EventPostToolUse, Tool: "bash",
			ToolInput: []byte(`{"command":"ok"}`), ToolResponse: exitStatus0},
	}
	d := AggregateDetail(records, now)
	tl := d.Sessions[0].Timeline
	// timeline[0] = preToolUse for "fail" — should have ErrorDetail set
	if tl[0].ErrorDetail != "exit 1: permission denied" {
		t.Errorf("timeline[0].ErrorDetail = %q; want %q", tl[0].ErrorDetail, "exit 1: permission denied")
	}
	// timeline[2] = preToolUse for "ok" — should have empty ErrorDetail
	if tl[2].ErrorDetail != "" {
		t.Errorf("timeline[2].ErrorDetail = %q; want empty", tl[2].ErrorDetail)
	}
}
