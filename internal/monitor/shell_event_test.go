package monitor

import (
	"testing"
)

func TestHasShellEvent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		timeline []EventEntry
		want     bool
	}{
		{"bare shell", []EventEntry{{Tool: "shell"}}, true},
		{"classified shell prefix", []EventEntry{{Tool: "shell:git push"}}, true},
		{"only write/read", []EventEntry{{Tool: "write"}, {Tool: "read"}}, false},
		{"empty timeline", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := SessionDetail{Timeline: c.timeline}
			if got := HasShellEvent(s); got != c.want {
				t.Errorf("HasShellEvent = %v, want %v", got, c.want)
			}
		})
	}
}
