package monitor

import (
	"encoding/json"
	"path/filepath"
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
// If path is relative and cwd is non-empty, joins with cwd.
// Always applies filepath.Clean.
func normalizeChangePath(path, cwd string) string {
	if !filepath.IsAbs(path) && cwd != "" {
		return filepath.Clean(filepath.Join(cwd, path))
	}
	return filepath.Clean(path)
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
