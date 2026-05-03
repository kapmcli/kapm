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

func TestParseIDEFileChange(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		tool        string
		raw         string
		wantOk      bool
		wantCommand string
		wantPath    string
		wantContent string
		wantOldStr  string
		wantNewStr  string
		wantPurpose string
	}{
		{
			name:        "create with content",
			tool:        ActionCreate,
			raw:         `{"file":"tmp-test.txt","originalContent":"","modifiedContent":"hello\n"}`,
			wantOk:      true,
			wantCommand: CommandCreate,
			wantPath:    "/repo/tmp-test.txt",
			wantContent: "hello\n",
		},
		{
			name:        "write with before after",
			tool:        ActionWrite,
			raw:         `{"file":"a.txt","originalContent":"old\n","modifiedContent":"new\n"}`,
			wantOk:      true,
			wantCommand: CommandStrReplace,
			wantPath:    "/repo/a.txt",
			wantOldStr:  "old\n",
			wantNewStr:  "new\n",
		},
		{
			name:        "delete without original content still records file",
			tool:        ActionDelete,
			raw:         `{"file":"tmp-test.txt","why":"cleanup"}`,
			wantOk:      true,
			wantCommand: CommandDelete,
			wantPath:    "/repo/tmp-test.txt",
			wantPurpose: "cleanup",
		},
		{
			name:   "missing file",
			tool:   ActionCreate,
			raw:    `{"modifiedContent":"x"}`,
			wantOk: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fc, ok := parseIDEFileChange(json.RawMessage(c.raw), c.tool, changeTs, "/repo")
			if ok != c.wantOk {
				t.Fatalf("ok = %v, want %v", ok, c.wantOk)
			}
			if !ok {
				return
			}
			if fc.Command != c.wantCommand || fc.Path != c.wantPath || fc.Content != c.wantContent || fc.OldStr != c.wantOldStr || fc.NewStr != c.wantNewStr || fc.Purpose != c.wantPurpose {
				t.Fatalf("FileChange = %#v", fc)
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
			name:     "delete with original content",
			fc:       FileChange{Command: CommandDelete, OldStr: "a\nb\n"},
			wantAdds: 0, wantDels: 2, wantOk: true,
		},
		{
			name:   "delete without original content unavailable",
			fc:     FileChange{Command: CommandDelete},
			wantOk: false,
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

func TestPrepareSessionChanges(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	t2 := t0.Add(2 * time.Minute)

	t.Run("empty input returns empty output", func(t *testing.T) {
		t.Parallel()
		got := prepareSessionChanges(nil)
		if len(got) != 0 {
			t.Fatalf("len = %d; want 0", len(got))
		}
	})

	t.Run("single file single edit aggregates correctly", func(t *testing.T) {
		t.Parallel()
		fc := FileChange{Path: "/a.go", Ts: t0, Command: "create", Content: "line1\nline2\n"}
		got := prepareSessionChanges([]FileChange{fc})
		if len(got) != 1 {
			t.Fatalf("len = %d; want 1", len(got))
		}
		g := got[0]
		if g.Path != "/a.go" {
			t.Errorf("Path = %q; want /a.go", g.Path)
		}
		if len(g.Edits) != 1 {
			t.Errorf("Edits len = %d; want 1", len(g.Edits))
		}
		if !g.LastTs.Equal(t0) {
			t.Errorf("LastTs = %v; want %v", g.LastTs, t0)
		}
		if g.TotalAdds != 2 {
			t.Errorf("TotalAdds = %d; want 2", g.TotalAdds)
		}
		if g.TotalDels != 0 {
			t.Errorf("TotalDels = %d; want 0", g.TotalDels)
		}
		if g.OversizedCount != 0 {
			t.Errorf("OversizedCount = %d; want 0", g.OversizedCount)
		}
	})

	t.Run("multiple files sorted by LastTs desc ties by path asc", func(t *testing.T) {
		t.Parallel()
		changes := []FileChange{
			{Path: "/b.go", Ts: t0, Command: "create", Content: "x\n"},
			{Path: "/a.go", Ts: t1, Command: "create", Content: "x\n"},
			{Path: "/c.go", Ts: t0, Command: "create", Content: "x\n"},
		}
		got := prepareSessionChanges(changes)
		if len(got) != 3 {
			t.Fatalf("len = %d; want 3", len(got))
		}
		// /a.go has latest ts (t1), then /b.go and /c.go tie at t0 → sorted by path asc
		if got[0].Path != "/a.go" {
			t.Errorf("got[0].Path = %q; want /a.go", got[0].Path)
		}
		if got[1].Path != "/b.go" {
			t.Errorf("got[1].Path = %q; want /b.go", got[1].Path)
		}
		if got[2].Path != "/c.go" {
			t.Errorf("got[2].Path = %q; want /c.go", got[2].Path)
		}
	})

	t.Run("mixed oversized and normal edits", func(t *testing.T) {
		t.Parallel()
		changes := []FileChange{
			{Path: "/a.go", Ts: t0, Command: "create", Content: "line1\n", Oversized: false},
			{Path: "/a.go", Ts: t1, Command: "create", Oversized: true},
		}
		got := prepareSessionChanges(changes)
		if len(got) != 1 {
			t.Fatalf("len = %d; want 1", len(got))
		}
		g := got[0]
		if g.TotalAdds != 1 {
			t.Errorf("TotalAdds = %d; want 1", g.TotalAdds)
		}
		if g.TotalDels != 0 {
			t.Errorf("TotalDels = %d; want 0", g.TotalDels)
		}
		if g.OversizedCount != 1 {
			t.Errorf("OversizedCount = %d; want 1", g.OversizedCount)
		}
	})

	t.Run("multiple edits same file grouped with latest LastTs", func(t *testing.T) {
		t.Parallel()
		changes := []FileChange{
			{Path: "/a.go", Ts: t0, Command: "create", Content: "x\n"},
			{Path: "/a.go", Ts: t2, Command: "strReplace", OldStr: "x\n", NewStr: "y\nz\n"},
		}
		got := prepareSessionChanges(changes)
		if len(got) != 1 {
			t.Fatalf("len = %d; want 1", len(got))
		}
		g := got[0]
		if len(g.Edits) != 2 {
			t.Errorf("Edits len = %d; want 2", len(g.Edits))
		}
		if !g.LastTs.Equal(t2) {
			t.Errorf("LastTs = %v; want %v", g.LastTs, t2)
		}
		// create: +1, strReplace old=1 new=2: +2/-1 → total +3/-1
		if g.TotalAdds != 3 {
			t.Errorf("TotalAdds = %d; want 3", g.TotalAdds)
		}
		if g.TotalDels != 1 {
			t.Errorf("TotalDels = %d; want 1", g.TotalDels)
		}
	})
}
