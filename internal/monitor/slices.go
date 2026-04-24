package monitor

// first returns s[0] and true when s is non-empty; otherwise the zero value and false.
func first[T any](s []T) (T, bool) {
	if len(s) == 0 {
		var zero T
		return zero, false
	}
	return s[0], true
}
