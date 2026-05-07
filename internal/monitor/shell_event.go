package monitor

// HasShellEvent reports whether the session used any shell tool invocation.
// The result is precomputed during aggregation; this wrapper exists for
// template function registration in serve/templates.go.
func HasShellEvent(s SessionDetail) bool {
	return s.HasShell
}
