package monitor

import (
	"testing"
)

func TestHasShellEvent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		hasShell bool
		want     bool
	}{
		{"bare shell", true, true},
		{"classified shell prefix", true, true},
		{"only write/read", false, false},
		{"empty timeline", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := SessionDetail{HasShell: c.hasShell}
			if got := HasShellEvent(s); got != c.want {
				t.Errorf("HasShellEvent = %v, want %v", got, c.want)
			}
		})
	}
}
