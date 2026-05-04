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
	Command     string `json:"command"`
	Path        string `json:"path"`
	Content     string `json:"content"`
	FileText    string `json:"file_text"`
	OldStr      string `json:"oldStr"`
	OldStrSnake string `json:"old_str"`
	NewStr      string `json:"newStr"`
	NewStrSnake string `json:"new_str"`
	Purpose     string `json:"__tool_use_purpose"`
}

type ideFileInput struct {
	File            string `json:"file"`
	Path            string `json:"path"`
	Local           string `json:"local"`
	OriginalContent string `json:"originalContent"`
	ModifiedContent string `json:"modifiedContent"`
	Why             string `json:"why"`
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
	command := normalizeWriteCommand(in.Command)
	switch command {
	case CommandCreate, CommandStrReplace, CommandInsert:
	default:
		return FileChange{}, false
	}
	content := firstNonEmptyWriteField(in.Content, in.FileText)
	oldStr := firstNonEmptyWriteField(in.OldStr, in.OldStrSnake)
	newStr := firstNonEmptyWriteField(in.NewStr, in.NewStrSnake)

	fc := FileChange{
		Path:    normalizeChangePath(in.Path, cwd),
		Ts:      ts,
		Command: command,
		Purpose: in.Purpose,
	}

	if len(content) > maxWriteFieldBytes {
		content = ""
		fc.Oversized = true
	}
	if len(oldStr) > maxWriteFieldBytes {
		oldStr = ""
		fc.Oversized = true
	}
	if len(newStr) > maxWriteFieldBytes {
		newStr = ""
		fc.Oversized = true
	}
	fc.Content = content
	fc.OldStr = oldStr
	fc.NewStr = newStr

	return fc, true
}

func normalizeWriteCommand(command string) string {
	if command == "str_replace" {
		return CommandStrReplace
	}
	return command
}

func firstNonEmptyWriteField(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func parseIDEFileChange(raw json.RawMessage, tool string, ts time.Time, cwd string) (FileChange, bool) {
	if len(raw) == 0 {
		return FileChange{}, false
	}
	var in ideFileInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return FileChange{}, false
	}
	p := in.File
	if p == "" {
		p = in.Path
	}
	if p == "" {
		return FileChange{}, false
	}

	fc := FileChange{Path: normalizeChangePath(p, cwd), Ts: ts, Purpose: in.Why}
	switch tool {
	case ActionCreate:
		fc.Command = CommandCreate
		fc.Content = in.ModifiedContent
	case ActionWrite:
		fc.Command = CommandStrReplace
		fc.OldStr = in.OriginalContent
		fc.NewStr = in.ModifiedContent
	case ActionDelete:
		fc.Command = CommandDelete
		fc.OldStr = in.OriginalContent
	default:
		return FileChange{}, false
	}

	if len(fc.Content) > maxWriteFieldBytes {
		fc.Content = ""
		fc.Oversized = true
	}
	if len(fc.OldStr) > maxWriteFieldBytes {
		fc.OldStr = ""
		fc.Oversized = true
	}
	if len(fc.NewStr) > maxWriteFieldBytes {
		fc.NewStr = ""
		fc.Oversized = true
	}
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
	case CommandDelete:
		if fc.OldStr == "" {
			return 0, 0, false
		}
		n := strings.Count(fc.OldStr, "\n")
		if !strings.HasSuffix(fc.OldStr, "\n") {
			n++
		}
		return 0, n, true
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
