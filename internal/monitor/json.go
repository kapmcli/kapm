package monitor

import (
	"encoding/json"
	"time"
)

// JSONDuration wraps time.Duration for human-readable JSON serialization.
type JSONDuration time.Duration

// String returns the Go standard duration string (e.g. "5m0s") so templates
// render it as a readable value instead of the underlying int64 nanoseconds.
func (d JSONDuration) String() string {
	return time.Duration(d).String()
}

func (d JSONDuration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *JSONDuration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		// fallback: try numeric nanoseconds
		var ns int64
		if err2 := json.Unmarshal(b, &ns); err2 != nil {
			return err
		}
		*d = JSONDuration(ns)
		return nil
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = JSONDuration(dur)
	return nil
}
