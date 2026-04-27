package serve

import (
	"strings"
	"testing"

	"github.com/kapmcli/kapm/internal/monitor"
)

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

