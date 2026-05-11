package monitor

import (
	"path/filepath"
	"runtime"
	"strings"
)

// abbrevHome replaces the user's home-directory prefix with "~".
func abbrevHome(home, p string) string {
	if home == "" {
		return p
	}
	home = filepath.Clean(home)
	p = filepath.Clean(p)
	if samePathText(p, home) {
		return "~"
	}
	if suffix, ok := trimPathPrefix(p, home); ok {
		return "~" + string(filepath.Separator) + suffix
	}
	return p
}

// trimPathPrefix strips prefix + separator, returning the remainder and true on match.
func trimPathPrefix(p, prefix string) (string, bool) {
	prefix = strings.TrimRight(prefix, `/\`)
	if prefix == "" || len(p) <= len(prefix) {
		return "", false
	}
	if !samePathText(p[:len(prefix)], prefix) || !isPathSeparator(p[len(prefix)]) {
		return "", false
	}
	return p[len(prefix)+1:], true
}

// samePathText compares two path strings with OS-aware case sensitivity.
func samePathText(a, b string) bool {
	a = strings.ReplaceAll(a, `\`, `/`)
	b = strings.ReplaceAll(b, `\`, `/`)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// isPathSeparator returns true for '/' and '\\'.
func isPathSeparator(ch byte) bool {
	return ch == '/' || ch == '\\'
}

// truncateLeft returns a string whose visible length is <= n, keeping the
// right-most characters and prefixing with "…" when truncated.
func truncateLeft(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return "…" + s[len(s)-(n-1):]
}
