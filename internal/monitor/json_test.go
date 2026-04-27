package monitor

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

func TestJSONDuration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Minute, `"5m0s"`},
		{350 * time.Millisecond, `"350ms"`},
		{0, `"0s"`},
		{90 * time.Second, `"1m30s"`},
	}
	for _, tc := range cases {
		jd := JSONDuration(tc.d)
		b, err := json.Marshal(jd)
		if err != nil {
			t.Fatalf("MarshalJSON(%v): %v", tc.d, err)
		}
		if string(b) != tc.want {
			t.Errorf("MarshalJSON(%v): got %s, want %s", tc.d, b, tc.want)
		}
		var got JSONDuration
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("UnmarshalJSON(%s): %v", b, err)
		}
		if got != jd {
			t.Errorf("round-trip %v: got %v, want %v", tc.d, time.Duration(got), tc.d)
		}
	}

	// fallback: numeric nanoseconds
	var got JSONDuration
	if err := json.Unmarshal([]byte("300000000"), &got); err != nil {
		t.Fatalf("UnmarshalJSON numeric: %v", err)
	}
	if time.Duration(got) != 300*time.Millisecond {
		t.Errorf("numeric fallback: got %v, want 300ms", time.Duration(got))
	}
}

func TestDetailedMetricsJSON(t *testing.T) {
	t.Parallel()
	now := baseTime.Add(10 * time.Minute)
	records := []Record{
		rec("s1", "agent1", apmconfig.EventAgentSpawn, "", 0),
		rec("s1", "agent1", apmconfig.EventPreToolUse, "bash", 2*time.Minute),
		rec("s1", "agent1", apmconfig.EventPostToolUse, "bash", 7*time.Minute),
		rec("s1", "agent1", apmconfig.EventStop, "", 9*time.Minute),
	}
	dm := mustAggregate(t, records, now)

	b, err := json.Marshal(dm)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	// Verify session Duration is a string in JSON output.
	overview, _ := raw["Overview"].(map[string]any)
	sessions, _ := overview["Sessions"].([]any)
	if len(sessions) == 0 {
		t.Fatal("expected at least one session")
	}
	s0, _ := sessions[0].(map[string]any)
	durVal, ok := s0["Duration"]
	if !ok {
		t.Fatal("Duration field missing from session JSON")
	}
	if _, isStr := durVal.(string); !isStr {
		t.Errorf("Duration should be a string in JSON, got %T: %v", durVal, durVal)
	}
}
