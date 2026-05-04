package monitor

import (
	"fmt"
	"time"
)

// FormatDuration formats d as "-" for missing/zero durations, then as
// "500ms", "12s", "1m03s", "1h02m", or "2d3h".
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	h := int(d.Hours())
	mins := int(d.Minutes()) % 60
	if h < 24 {
		return fmt.Sprintf("%dh%02dm", h, mins)
	}
	days := h / 24
	return fmt.Sprintf("%dd%dh", days, h%24)
}
