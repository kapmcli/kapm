package monitor

import (
	"cmp"
	"encoding/json"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	udiff "github.com/aymanbagabas/go-udiff"
)

const maxWriteFieldBytes = 256 << 10 // per-field cap to bound memory

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
	case CommandCreate, CommandStrReplace, CommandInsert:
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

// PathGroup holds aggregated change data for a single file path,
// ready for rendering.
type PathGroup struct {
	Path           string
	Edits          []FileChange
	LastTs         time.Time
	TotalAdds      int
	TotalDels      int
	OversizedCount int
}

// prepareSessionChanges groups changes by path, sorts by LastTs desc (ties by
// path asc), and pre-computes TotalAdds, TotalDels, OversizedCount per group.
func prepareSessionChanges(changes []FileChange) []PathGroup {
	if len(changes) == 0 {
		return nil
	}
	paths := make([]string, 0)
	grouped := map[string][]FileChange{}
	for _, fc := range changes {
		if _, ok := grouped[fc.Path]; !ok {
			paths = append(paths, fc.Path)
		}
		grouped[fc.Path] = append(grouped[fc.Path], fc)
	}
	slices.SortFunc(paths, func(a, b string) int {
		lastA := grouped[a][len(grouped[a])-1].Ts
		lastB := grouped[b][len(grouped[b])-1].Ts
		if c := lastB.Compare(lastA); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	groups := make([]PathGroup, len(paths))
	for i, p := range paths {
		edits := grouped[p]
		var totalAdds, totalDels, oversizedCount int
		for _, fc := range edits {
			if a, d, ok := DiffLineCounts(fc); ok {
				totalAdds += a
				totalDels += d
			} else if fc.Oversized {
				oversizedCount++
			}
		}
		groups[i] = PathGroup{
			Path:           p,
			Edits:          edits,
			LastTs:         edits[len(edits)-1].Ts,
			TotalAdds:      totalAdds,
			TotalDels:      totalDels,
			OversizedCount: oversizedCount,
		}
	}
	return groups
}

// DiffLineCounts returns the number of added and deleted lines for a FileChange.
// ok=false when counts cannot be computed (Oversized, non-UTF8, unknown command).
func DiffLineCounts(fc FileChange) (adds, dels int, ok bool) {
	if fc.Oversized {
		return 0, 0, false
	}
	if !utf8.ValidString(fc.Content) || !utf8.ValidString(fc.OldStr) || !utf8.ValidString(fc.NewStr) {
		return 0, 0, false
	}
	switch fc.Command {
	case CommandCreate, CommandInsert:
		if fc.Content == "" {
			return 0, 0, true
		}
		n := strings.Count(fc.Content, "\n")
		if !strings.HasSuffix(fc.Content, "\n") {
			n++
		}
		return n, 0, true
	case CommandStrReplace:
		diffStr := udiff.Unified("", "", fc.OldStr, fc.NewStr)
		for line := range strings.SplitSeq(diffStr, "\n") {
			if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "@@") {
				continue
			}
			if len(line) == 0 {
				continue
			}
			switch line[0] {
			case '+':
				adds++
			case '-':
				dels++
			}
		}
		return adds, dels, true
	default:
		return 0, 0, false
	}
}
