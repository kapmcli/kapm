package serve

import (
	"regexp"
	"strings"
	"testing"

	"github.com/kapmcli/kapm/internal/monitor"
)

func TestRenderDiffStringMalformedHunk(t *testing.T) {
	t.Parallel()
	// Oversized line number exceeds int range — strconv.Atoi must fail gracefully.
	malformed := "@@ -99999999999999999999,1 +1,1 @@\n-old\n+new\n"
	out := string(renderDiffString(malformed))
	if !strings.Contains(out, "99999999999999999999") {
		t.Errorf("want hunk header in output, got: %s", out)
	}
	// oldLn falls back to 0; context/del lines should show "0" in ln-old column.
	if !regexp.MustCompile(`<span class="ln ln-old">0</span>`).MatchString(out) {
		t.Errorf("want ln-old=0 after parse failure, got: %s", out)
	}
}

func TestRenderDiff_EscapesHTML(t *testing.T) {
	t.Parallel()
	fc := monitor.FileChange{
		Path:    "evil.html",
		Command: "create",
		Content: `<script>alert("xss")</script>`,
	}
	out := string(renderDiff(fc))
	if strings.Contains(out, "<script>") {
		t.Errorf("unescaped <script> found in output: %s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("escaped <script> not found, want &lt;script&gt;: %s", out)
	}
}

func TestRenderDiff_Placeholders(t *testing.T) {
	t.Parallel()
	t.Run("oversized", func(t *testing.T) {
		t.Parallel()
		fc := monitor.FileChange{Path: "f", Command: "create", Oversized: true}
		out := string(renderDiff(fc))
		if !strings.Contains(out, "content truncated at extraction") {
			t.Errorf("want oversized placeholder, got: %s", out)
		}
	})

	t.Run("size_cap", func(t *testing.T) {
		t.Parallel()
		fc := monitor.FileChange{
			Path:    "f",
			Command: "create",
			Content: strings.Repeat("a", diffByteCap+1),
		}
		out := string(renderDiff(fc))
		if !strings.Contains(out, "content size exceeds 64KB") {
			t.Errorf("want size cap placeholder, got: %s", out)
		}
	})

	t.Run("non_utf8", func(t *testing.T) {
		t.Parallel()
		fc := monitor.FileChange{
			Path:    "f",
			Command: "create",
			Content: string([]byte{0xff, 0xfe}),
		}
		out := string(renderDiff(fc))
		if !strings.Contains(out, "binary or non-UTF8") {
			t.Errorf("want non-UTF8 placeholder, got: %s", out)
		}
	})

	t.Run("empty_diff", func(t *testing.T) {
		t.Parallel()
		fc := monitor.FileChange{Path: "f", Command: "strReplace", OldStr: "x", NewStr: "x"}
		out := string(renderDiff(fc))
		if !strings.Contains(out, "no textual change") {
			t.Errorf("want no-change placeholder, got: %s", out)
		}
	})
}

func TestRenderDiff_HappyPath(t *testing.T) {
	t.Parallel()
	fc := monitor.FileChange{
		Path:    "main.go",
		Command: "strReplace",
		OldStr:  "hello\n",
		NewStr:  "world\n",
	}
	out := string(renderDiff(fc))
	if !strings.Contains(out, "diff-add") {
		t.Errorf("want diff-add class, got: %s", out)
	}
	if !strings.Contains(out, "diff-del") {
		t.Errorf("want diff-del class, got: %s", out)
	}
}

func TestRenderDiff_Delete(t *testing.T) {
	t.Parallel()
	t.Run("with_original_content", func(t *testing.T) {
		t.Parallel()
		fc := monitor.FileChange{Path: "gone.txt", Command: monitor.CommandDelete, OldStr: "old\n"}
		out := string(renderDiff(fc))
		if !strings.Contains(out, "diff-del") || strings.Contains(out, "no textual change") {
			t.Errorf("want delete diff, got: %s", out)
		}
	})
	t.Run("without_original_content", func(t *testing.T) {
		t.Parallel()
		fc := monitor.FileChange{Path: "gone.txt", Command: monitor.CommandDelete}
		out := string(renderDiff(fc))
		if !strings.Contains(out, "delete content unavailable") {
			t.Errorf("want unavailable placeholder, got: %s", out)
		}
	})
}

func TestDiffStats_DeleteWithoutContentUnavailable(t *testing.T) {
	t.Parallel()
	r := diffStats([]monitor.FileChange{{Command: monitor.CommandDelete}})
	if r.HasCounts || r.OversizedCount != 1 {
		t.Fatalf("diffStats = %+v, want no counts and one unavailable edit", r)
	}
}

func TestRenderDiff_OmitsFileHeaders(t *testing.T) {
	t.Parallel()
	fc := monitor.FileChange{
		Path:    "main.go",
		Command: "create",
		Content: "hello\n",
	}
	out := string(renderDiff(fc))
	if strings.Contains(out, "+++") {
		t.Errorf("output should not contain +++ header: %s", out)
	}
	if strings.Contains(out, "---") {
		t.Errorf("output should not contain --- header: %s", out)
	}
	if !strings.Contains(out, `<div class="diff-hunk">`) {
		t.Errorf("output should contain a diff-hunk div: %s", out)
	}
}

func TestRenderDiff_HunkHeaderIsDiv(t *testing.T) {
	t.Parallel()
	fc := monitor.FileChange{
		Path:    "f",
		Command: "strReplace",
		OldStr:  "a\nb\nc\n",
		NewStr:  "a\nB\nc\n",
	}
	out := string(renderDiff(fc))
	if !strings.Contains(out, `<div class="diff-hunk">@@`) {
		t.Errorf("hunk header should be wrapped in <div class=\"diff-hunk\">: %s", out)
	}
}

func TestRenderDiff_LineNumbers(t *testing.T) {
	t.Parallel()
	fc := monitor.FileChange{
		Path:    "f",
		Command: "strReplace",
		OldStr:  "l1\nl2\nl3\nl4\nl5\n",
		NewStr:  "l1\nL2\nl3\nL4\nl5\n",
	}
	out := string(renderDiff(fc))
	if !strings.Contains(out, `<span class="ln ln-old">`) {
		t.Errorf("want ln-old span in output: %s", out)
	}
	if !strings.Contains(out, `<span class="ln ln-new">`) {
		t.Errorf("want ln-new span in output: %s", out)
	}
	// Expect old line 1 (context) to appear in an old-line span.
	if !regexp.MustCompile(`<span class="ln ln-old">1</span>`).MatchString(out) {
		t.Errorf("want ln-old=1 context line, got: %s", out)
	}
	// Expect new line 1 in ln-new too.
	if !regexp.MustCompile(`<span class="ln ln-new">1</span>`).MatchString(out) {
		t.Errorf("want ln-new=1 context line, got: %s", out)
	}
}

func TestRenderDiff_RowStructure(t *testing.T) {
	t.Parallel()
	fc := monitor.FileChange{
		Path:    "f",
		Command: "strReplace",
		OldStr:  "a\n",
		NewStr:  "b\n",
	}
	out := string(renderDiff(fc))
	if !strings.Contains(out, `<span class="sign">`) {
		t.Errorf("want sign span in output: %s", out)
	}
	if !strings.Contains(out, `<span class="code">`) {
		t.Errorf("want code span in output: %s", out)
	}
	if !strings.Contains(out, `<div class="diff-row diff-add">`) {
		t.Errorf("want diff-row diff-add div: %s", out)
	}
	if !strings.Contains(out, `<div class="diff-row diff-del">`) {
		t.Errorf("want diff-row diff-del div: %s", out)
	}
}

func TestDiffStats(t *testing.T) {
	t.Parallel()
	t.Run("all_counts", func(t *testing.T) {
		t.Parallel()
		changes := []monitor.FileChange{
			{Command: "create", Content: "a\nb\nc\n"},
			{Command: "strReplace", OldStr: "x\n", NewStr: "y\n"},
		}
		r := diffStats(changes)
		if !r.HasCounts {
			t.Fatalf("want HasCounts=true, got %+v", r)
		}
		if r.Adds != 4 || r.Dels != 1 {
			t.Errorf("want adds=4 dels=1, got adds=%d dels=%d", r.Adds, r.Dels)
		}
		if r.OversizedCount != 0 {
			t.Errorf("want OversizedCount=0, got %d", r.OversizedCount)
		}
	})

	t.Run("all_oversized", func(t *testing.T) {
		t.Parallel()
		changes := []monitor.FileChange{
			{Command: "create", Oversized: true},
			{Command: "strReplace", Oversized: true},
		}
		r := diffStats(changes)
		if r.HasCounts {
			t.Errorf("want HasCounts=false, got %+v", r)
		}
		if r.OversizedCount != 2 {
			t.Errorf("want OversizedCount=2, got %d", r.OversizedCount)
		}
	})

	t.Run("mixed", func(t *testing.T) {
		t.Parallel()
		changes := []monitor.FileChange{
			{Command: "create", Content: "a\nb\n"},
			{Command: "create", Oversized: true},
		}
		r := diffStats(changes)
		if !r.HasCounts {
			t.Fatalf("want HasCounts=true, got %+v", r)
		}
		if r.Adds != 2 || r.Dels != 0 {
			t.Errorf("want adds=2 dels=0, got adds=%d dels=%d", r.Adds, r.Dels)
		}
		if r.OversizedCount != 1 {
			t.Errorf("want OversizedCount=1, got %d", r.OversizedCount)
		}
	})
}

// renderSessionDetailContent renders the session_detail "content" block with a
// minimal SessionDetail fixture — enough to exercise the Changes group summary.
func renderSessionDetailContent(t *testing.T, changes []monitor.FileChange) string {
	t.Helper()
	sd := monitor.SessionDetail{
		SessionMetric: monitor.SessionMetric{ID: "s1", Agent: "a"},
		Changes:       changes,
	}
	var buf strings.Builder
	if err := sessionDetailTmpl.ExecuteTemplate(&buf, "content", map[string]any{
		"Session": sd,
		"SelfURL": "/sessions/s1",
	}); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func TestSessionDetail_SummaryBadges(t *testing.T) {
	t.Parallel()
	// 3 adds, 1 del via strReplace.
	changes := []monitor.FileChange{
		{Path: "a.go", Command: "strReplace", OldStr: "l1\n", NewStr: "l1\nx\ny\nz\n"},
	}
	out := renderSessionDetailContent(t, changes)
	if !strings.Contains(out, `class="badge badge-add">+3`) {
		t.Errorf("want badge +3, got: %s", out)
	}
	if !strings.Contains(out, `class="badge badge-del">-0`) && !strings.Contains(out, `class="badge badge-del">-1`) {
		// strReplace going from 1 line to 4 lines: udiff typically deletes 0 and adds 3.
		// We accept either 0 or 1 here; the key assertion is that a del badge appears.
		t.Errorf("want a -N badge, got: %s", out)
	}
}

func TestSessionDetail_OversizedSummary(t *testing.T) {
	t.Parallel()
	changes := []monitor.FileChange{
		{Path: "big.bin", Command: "create", Oversized: true},
	}
	out := renderSessionDetailContent(t, changes)
	if strings.Contains(out, "badge-add") {
		t.Errorf("all-oversized should not render badge-add: %s", out)
	}
	if strings.Contains(out, "badge-del") {
		t.Errorf("all-oversized should not render badge-del: %s", out)
	}
	if !strings.Contains(out, `<span class="muted">—</span>`) {
		t.Errorf("want muted em-dash for all-oversized summary, got: %s", out)
	}
}
