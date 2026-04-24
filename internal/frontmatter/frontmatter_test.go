package frontmatter

import (
	"strings"
	"testing"
)

func TestParseWithFrontMatter(t *testing.T) {
	t.Parallel()

	content := "---\napplyTo: \"**\"\ndescription: test\n---\n\n# Title\n"

	doc, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if got, want := doc.Meta["applyTo"], "**"; got != want {
		t.Fatalf("Meta[applyTo] = %v, want %q", got, want)
	}
	if got, want := doc.Meta["description"], "test"; got != want {
		t.Fatalf("Meta[description] = %v, want %q", got, want)
	}
	if got, want := doc.Body, "\n# Title\n"; got != want {
		t.Fatalf("Body = %q, want %q", got, want)
	}
}

func TestParseWithoutFrontMatter(t *testing.T) {
	t.Parallel()

	content := "# Title\n"

	doc, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if doc.Meta != nil {
		t.Fatalf("Meta = %#v, want nil", doc.Meta)
	}
	if got, want := doc.Body, content; got != want {
		t.Fatalf("Body = %q, want %q", got, want)
	}
}

func TestParseEmptyFrontMatter(t *testing.T) {
	t.Parallel()

	content := "---\n---\nbody\n"

	doc, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(doc.Meta) != 0 {
		t.Fatalf("len(Meta) = %d, want 0", len(doc.Meta))
	}
	if got, want := doc.Body, "body\n"; got != want {
		t.Fatalf("Body = %q, want %q", got, want)
	}
}

func TestParseFrontMatterOnly(t *testing.T) {
	t.Parallel()

	content := "---\nname: kapm\n---\n"

	doc, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got, want := doc.Meta["name"], "kapm"; got != want {
		t.Fatalf("Meta[name] = %v, want %q", got, want)
	}
	if got := doc.Body; got != "" {
		t.Fatalf("Body = %q, want empty", got)
	}
}

func TestParseRejectsOversizedFrontMatter(t *testing.T) {
	t.Parallel()

	// Build a content string with front matter exceeding 256 KiB
	meta := strings.Repeat("x", (256<<10)+1)
	content := "---\n" + meta + "\n---\nbody\n"

	if _, err := Parse(content); err == nil {
		t.Fatal("Parse() error = nil, want error for oversized front matter")
	}
}

func TestParseMissingClosingDelimiter(t *testing.T) {
	t.Parallel()

	content := "---\nname: kapm\nbody\n"

	if _, err := Parse(content); err == nil {
		t.Fatal("Parse() error = nil, want error")
	}
}
