package convert

import "fmt"

// Report captures converter output counts.
type Report struct {
	Converted int
	Skipped   int
}

// Add accumulates another converter report into r.
func (r *Report) Add(other Report) {
	r.Converted += other.Converted
	r.Skipped += other.Skipped
}

// wrapConvertError wraps err with a "convert <kind> <key>: " prefix.
func wrapConvertError(kind, key string, err error) error {
	return fmt.Errorf("convert %s %q: %w", kind, key, err)
}
