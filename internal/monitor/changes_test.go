package monitor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var changeTs = time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC)

func TestParseWriteInput(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", maxWriteFieldBytes+1)

	cases := []struct {
		name        string
		raw         string
		cwd         string
		wantOk      bool
		wantCommand string
		wantPath    string
		wantContent string
		wantOldStr  string
		wantNewStr  string
		wantPurpose string
		wantOver    bool
	}{
		// Happy path: create
		{
			name:        "create",
			raw:         `{"command":"create","path":"/tmp/foo.txt","content":"hello"}`,
			wantOk:      true,
			wantCommand: "create",
			wantPath:    "/tmp/foo.txt",
			wantContent: "hello",
		},
		// Happy path: strReplace
		{
			name:        "strReplace",
			raw:         `{"command":"strReplace","path":"/tmp/bar.go","oldStr":"old","newStr":"new"}`,
			wantOk:      true,
			wantCommand: "strReplace",
			wantPath:    "/tmp/bar.go",
			wantOldStr:  "old",
			wantNewStr:  "new",
		},
		// Happy path: insert (treated as append at EOF)
		{
			name:        "insert",
			raw:         `{"command":"insert","path":"/tmp/baz.txt","content":"appended"}`,
			wantOk:      true,
			wantCommand: "insert",
			wantPath:    "/tmp/baz.txt",
			wantContent: "appended",
		},
		// __tool_use_purpose present
		{
			name:        "with purpose",
			raw:         `{"command":"create","path":"/a.txt","content":"x","__tool_use_purpose":"write the file"}`,
			wantOk:      true,
			wantCommand: "create",
			wantPath:    "/a.txt",
			wantContent: "x",
			wantPurpose: "write the file",
		},
		// __tool_use_purpose absent
		{
			name:        "without purpose",
			raw:         `{"command":"create","path":"/a.txt","content":"x"}`,
			wantOk:      true,
			wantCommand: "create",
			wantPath:    "/a.txt",
			wantContent: "x",
			wantPurpose: "",
		},
		// Malformed JSON → skip
		{
			name:   "malformed JSON",
			raw:    `{not valid json`,
			wantOk: false,
		},
		// Non-object (array) → skip
		{
			name:   "non_object array",
			raw:    `["create","path"]`,
			wantOk: false,
		},
		// null → skip
		{
			name:   "null",
			raw:    `null`,
			wantOk: false,
		},
		// Empty → skip
		{
			name:   "empty",
			raw:    ``,
			wantOk: false,
		},
		// Missing path → skip
		{
			name:   "missing path",
			raw:    `{"command":"create","content":"hi"}`,
			wantOk: false,
		},
		// Unknown command → skip
		{
			name:   "unknown command",
			raw:    `{"command":"delete","path":"/x"}`,
			wantOk: false,
		},
		// Missing command → skip
		{
			name:   "missing command",
			raw:    `{"path":"/x","content":"hi"}`,
			wantOk: false,
		},
		// Oversized content
		{
			name:        "oversized content",
			raw:         `{"command":"create","path":"/big.txt","content":"` + big + `"}`,
			wantOk:      true,
			wantCommand: "create",
			wantPath:    "/big.txt",
			wantContent: "",
			wantOver:    true,
		},
		// Oversized oldStr
		{
			name:        "oversized oldStr",
			raw:         `{"command":"strReplace","path":"/x.go","oldStr":"` + big + `","newStr":"y"}`,
			wantOk:      true,
			wantCommand: "strReplace",
			wantPath:    "/x.go",
			wantOldStr:  "",
			wantNewStr:  "y",
			wantOver:    true,
		},
		// Oversized newStr
		{
			name:        "oversized newStr",
			raw:         `{"command":"strReplace","path":"/x.go","oldStr":"x","newStr":"` + big + `"}`,
			wantOk:      true,
			wantCommand: "strReplace",
			wantPath:    "/x.go",
			wantOldStr:  "x",
			wantNewStr:  "",
			wantOver:    true,
		},
		// Path normalization: relative with cwd
		{
			name:        "relative path with cwd",
			raw:         `{"command":"create","path":"foo.txt","content":"x"}`,
			cwd:         "/tmp",
			wantOk:      true,
			wantCommand: "create",
			wantPath:    "/tmp/foo.txt",
			wantContent: "x",
		},
		// Path normalization: ./foo.txt with cwd same as foo.txt
		{
			name:        "dot-slash relative with cwd",
			raw:         `{"command":"create","path":"./foo.txt","content":"x"}`,
			cwd:         "/tmp",
			wantOk:      true,
			wantCommand: "create",
			wantPath:    "/tmp/foo.txt",
			wantContent: "x",
		},
		// Path normalization: absolute path ignores cwd
		{
			name:        "absolute path ignores cwd",
			raw:         `{"command":"create","path":"/abs/file.txt","content":"x"}`,
			cwd:         "/tmp",
			wantOk:      true,
			wantCommand: "create",
			wantPath:    "/abs/file.txt",
			wantContent: "x",
		},
		// Path normalization: relative without cwd
		{
			name:        "relative path no cwd",
			raw:         `{"command":"create","path":"./sub/file.txt","content":"x"}`,
			cwd:         "",
			wantOk:      true,
			wantCommand: "create",
			wantPath:    "sub/file.txt",
			wantContent: "x",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fc, ok := parseWriteInput(json.RawMessage(c.raw), changeTs, c.cwd)
			if ok != c.wantOk {
				t.Fatalf("ok = %v; want %v", ok, c.wantOk)
			}
			if !ok {
				return
			}
			if fc.Command != c.wantCommand {
				t.Errorf("Command = %q; want %q", fc.Command, c.wantCommand)
			}
			if fc.Path != c.wantPath {
				t.Errorf("Path = %q; want %q", fc.Path, c.wantPath)
			}
			if fc.Content != c.wantContent {
				t.Errorf("Content = %q; want %q", fc.Content, c.wantContent)
			}
			if fc.OldStr != c.wantOldStr {
				t.Errorf("OldStr = %q; want %q", fc.OldStr, c.wantOldStr)
			}
			if fc.NewStr != c.wantNewStr {
				t.Errorf("NewStr = %q; want %q", fc.NewStr, c.wantNewStr)
			}
			if fc.Purpose != c.wantPurpose {
				t.Errorf("Purpose = %q; want %q", fc.Purpose, c.wantPurpose)
			}
			if fc.Oversized != c.wantOver {
				t.Errorf("Oversized = %v; want %v", fc.Oversized, c.wantOver)
			}
			if !fc.Ts.Equal(changeTs) {
				t.Errorf("Ts = %v; want %v", fc.Ts, changeTs)
			}
		})
	}
}

func TestNormalizeChangePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		cwd  string
		want string
	}{
		{"absolute no cwd", "/tmp/foo.txt", "", "/tmp/foo.txt"},
		{"absolute with cwd", "/tmp/foo.txt", "/other", "/tmp/foo.txt"},
		{"relative with cwd", "foo.txt", "/tmp", "/tmp/foo.txt"},
		{"dot-slash with cwd", "./foo.txt", "/tmp", "/tmp/foo.txt"},
		{"relative no cwd", "foo.txt", "", "foo.txt"},
		{"dot-slash no cwd", "./foo.txt", "", "foo.txt"},
		{"double-dot cleaned", "/tmp/../etc/passwd", "", "/etc/passwd"},
		{"relative double-dot with cwd", "../sibling/file.go", "/tmp/sub", "/tmp/sibling/file.go"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeChangePath(c.path, c.cwd)
			if got != c.want {
				t.Errorf("normalizeChangePath(%q, %q) = %q; want %q", c.path, c.cwd, got, c.want)
			}
		})
	}
}

func TestCountUniqueFiles(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		changes []FileChange
		want    int
	}{
		{"nil", nil, 0},
		{"empty", []FileChange{}, 0},
		{"single", []FileChange{{Path: "/tmp/a.go"}}, 1},
		{"two distinct", []FileChange{{Path: "/tmp/a.go"}, {Path: "/tmp/b.go"}}, 2},
		// Same file written multiple times → 1 unique
		{
			name: "same file multiple writes",
			changes: []FileChange{
				{Path: "/tmp/foo.txt"},
				{Path: "/tmp/foo.txt"},
				{Path: "/tmp/foo.txt"},
			},
			want: 1,
		},
		// Relative ./foo.txt with cwd=/tmp and absolute /tmp/foo.txt are the same
		// after normalization at parse time — both stored as /tmp/foo.txt
		{
			name: "relative and absolute same file",
			changes: []FileChange{
				{Path: "/tmp/foo.txt"}, // was ./foo.txt with cwd=/tmp, normalized at parse
				{Path: "/tmp/foo.txt"}, // was /tmp/foo.txt, normalized at parse
			},
			want: 1,
		},
		{
			name: "mix of files",
			changes: []FileChange{
				{Path: "/tmp/a.go"},
				{Path: "/tmp/b.go"},
				{Path: "/tmp/a.go"},
				{Path: "/tmp/c.go"},
			},
			want: 3,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := countUniqueFiles(c.changes)
			if got != c.want {
				t.Errorf("countUniqueFiles = %d; want %d", got, c.want)
			}
		})
	}
}

func TestDiffLineCounts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		fc       FileChange
		wantAdds int
		wantDels int
		wantOk   bool
	}{
		{
			name:     "create 3-line content",
			fc:       FileChange{Command: "create", Content: "a\nb\nc\n"},
			wantAdds: 3, wantDels: 0, wantOk: true,
		},
		{
			name:     "strReplace with modification",
			fc:       FileChange{Command: "strReplace", OldStr: "foo\nbar\n", NewStr: "foo\nbaz\nqux\n"},
			wantAdds: 2, wantDels: 1, wantOk: true,
		},
		{
			name:   "oversized returns ok=false",
			fc:     FileChange{Command: "create", Oversized: true},
			wantOk: false,
		},
		{
			name:   "unknown command returns ok=false",
			fc:     FileChange{Command: "rename"},
			wantOk: false,
		},
		{
			name:     "empty content create",
			fc:       FileChange{Command: "create", Content: ""},
			wantAdds: 0, wantDels: 0, wantOk: true,
		},
		{
			name:   "non-UTF8 content returns ok=false",
			fc:     FileChange{Command: "create", Content: "\xff\xfe\xfd"},
			wantOk: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			adds, dels, ok := DiffLineCounts(c.fc)
			if ok != c.wantOk {
				t.Fatalf("ok = %v; want %v", ok, c.wantOk)
			}
			if !ok {
				return
			}
			if adds != c.wantAdds {
				t.Errorf("adds = %d; want %d", adds, c.wantAdds)
			}
			if dels != c.wantDels {
				t.Errorf("dels = %d; want %d", dels, c.wantDels)
			}
		})
	}
}
