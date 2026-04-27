package monitor

import (
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0ms"},
		{-5 * time.Second, "0ms"},
		{500 * time.Millisecond, "500ms"},
		{999 * time.Millisecond, "999ms"},
		{time.Second, "1s"},
		{12 * time.Second, "12s"},
		{59 * time.Second, "59s"},
		{time.Minute, "1m00s"},
		{63 * time.Second, "1m03s"},
		{90 * time.Second, "1m30s"},
		{time.Hour, "1h00m"},
		{time.Hour + 2*time.Minute, "1h02m"},
		{23*time.Hour + 59*time.Minute, "23h59m"},
		{24 * time.Hour, "1d0h"},
		{48*time.Hour + 3*time.Hour, "2d3h"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			got := FormatDuration(tc.d)
			if got != tc.want {
				t.Errorf("FormatDuration(%v) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}
