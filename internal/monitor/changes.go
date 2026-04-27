package monitor

import (
	"encoding/json"
	"path"
	"strings"
	"time"
)

const maxWriteFieldBytes = 256 * 1024 // per-field cap to bound memory

// writeInput is the typed view of the write tool's tool_input payload.
type writeInput struct {
	Command string `json:"command"`
	Path    string `json:"path"`
	Content string `json:"content"`
	OldStr  string `json:"oldStr"`
	NewStr  string `json:"newStr"`
	Purpose string `json:"__tool_use_purpose"`
}

// parseWriteInput extracts a FileChange from a preToolUse write record.
// Returns ok=false on malformed JSON, unknown command, or missing path.
// Normalizes Path at extraction time using cwd; truncates oversized content
// fields and sets Oversized=true.
func parseWriteInput(raw json.RawMessage, ts time.Time, cwd string) (FileChange, bool) {
	if len(raw) == 0 {
		return FileChange{}, false
	}
	var in writeInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return FileChange{}, false
	}
	if in.Path == "" {
		return FileChange{}, false
	}
	switch in.Command {
	case "create", "strReplace", "insert":
	default:
		return FileChange{}, false
	}

	fc := FileChange{
		Path:    normalizeChangePath(in.Path, cwd),
		Ts:      ts,
		Command: in.Command,
		Purpose: in.Purpose,
	}

	if len(in.Content) > maxWriteFieldBytes {
		in.Content = ""
		fc.Oversized = true
	}
	if len(in.OldStr) > maxWriteFieldBytes {
		in.OldStr = ""
		fc.Oversized = true
	}
	if len(in.NewStr) > maxWriteFieldBytes {
		in.NewStr = ""
		fc.Oversized = true
	}
	fc.Content = in.Content
	fc.OldStr = in.OldStr
	fc.NewStr = in.NewStr

	return fc, true
}

// normalizeChangePath returns a canonical key for uniqueness comparison.
// Paths in JSON logs use forward slashes regardless of host OS, so we use
// the stdlib `path` package (always /) instead of `filepath` (OS-dependent)
// to keep behavior identical on Linux, macOS, and Windows.
// If path is relative and cwd is non-empty, joins with cwd (also slash-based).
// Always applies path.Clean.
func normalizeChangePath(p, cwd string) string {
	if !isSlashAbs(p) && cwd != "" {
		return path.Clean(path.Join(cwd, p))
	}
	return path.Clean(p)
}

// isSlashAbs reports whether p is absolute in forward-slash logical path
// terms. It treats a leading "/" as absolute on every OS, which matches the
// paths recorded by kapm agents across platforms.
func isSlashAbs(p string) bool {
	return strings.HasPrefix(p, "/")
}

// countUniqueFiles returns the number of distinct Path values
// (paths are already normalized at parse time).
func countUniqueFiles(changes []FileChange) int {
	seen := make(map[string]struct{}, len(changes))
	for _, fc := range changes {
		seen[fc.Path] = struct{}{}
	}
	return len(seen)
}
