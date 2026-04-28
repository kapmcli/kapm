package monitor

import (
	"cmp"
	"slices"
)

// first returns s[0] and true when s is non-empty; otherwise the zero value and false.
func first[T any](s []T) (T, bool) {
	if len(s) == 0 {
		var zero T
		return zero, false
	}
	return s[0], true
}

// finalizeToolAgg converts a tool aggregation map to a sorted SessionToolSummary slice.
func finalizeToolAgg(m map[string]*toolAgg) []SessionToolSummary {
	if len(m) == 0 {
		return nil
	}
	out := make([]SessionToolSummary, 0, len(m))
	for tool, a := range m {
		out = append(out, toolAggToSummary(tool, a))
	}
	slices.SortFunc(out, func(a, b SessionToolSummary) int {
		if c := cmp.Compare(b.CallCount, a.CallCount); c != 0 {
			return c
		}
		return cmp.Compare(a.Tool, b.Tool)
	})
	return out
}
