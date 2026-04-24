package monitor

import (
	"fmt"
	"strconv"
	"time"
)

// ParseDuration parses a duration string with suffix s, m, h, d, or w.
func ParseDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	suffix := s[len(s)-1]
	n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	switch suffix {
	case 's':
		return time.Duration(n) * time.Second, nil
	case 'm':
		return time.Duration(n) * time.Minute, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(n) * 168 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid duration %q", s)
	}
}
