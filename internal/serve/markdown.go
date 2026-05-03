package serve

import (
	"bytes"
	"html"
	"html/template"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

// linkRelRenderer adds rel="noopener nofollow" to all <a> tags via goldmark NodeRenderer.
type linkRelRenderer struct{ gmhtml.Config }

type escapedRawHTMLRenderer struct{}

func (r *linkRelRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindLink, r.renderLink)
	reg.Register(ast.KindAutoLink, r.renderAutoLink)
}

func (r *linkRelRenderer) renderAutoLink(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	al := n.(*ast.AutoLink)
	u := util.EscapeHTML(util.URLEscape(al.URL(source), false))
	_, _ = w.WriteString(`<a rel="noopener nofollow" href="`)
	_, _ = w.Write(u)
	_, _ = w.WriteString(`">`)
	_, _ = w.Write(util.EscapeHTML(al.Label(source)))
	_, _ = w.WriteString(`</a>`)
	return ast.WalkSkipChildren, nil
}

// schemeOf returns the lower-cased scheme and whether dest is a relative URL.
// A URL is considered relative when it has no ":" before the first "/" or "?".
func schemeOf(dest string) (scheme string, isRelative bool) {
	s := strings.TrimSpace(dest)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ':':
			return strings.ToLower(s[:i]), false
		case '/', '?', '#':
			return "", true
		}
	}
	return "", true
}

func (r *linkRelRenderer) renderLink(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("</a>")
		return ast.WalkContinue, nil
	}
	link := n.(*ast.Link)
	dest := string(link.Destination)
	scheme, isRel := schemeOf(dest)
	switch {
	case isRel, scheme == "http", scheme == "https", scheme == "mailto":
		// allowed
	default:
		_, _ = w.WriteString(`<a rel="noopener nofollow" href="#">`)
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString(`<a rel="noopener nofollow" href="`)
	_, _ = w.Write(util.EscapeHTML(util.URLEscape(link.Destination, true)))
	_, _ = w.WriteString(`">`)
	return ast.WalkContinue, nil
}

func (r *escapedRawHTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindHTMLBlock, r.renderHTMLBlock)
	reg.Register(ast.KindRawHTML, r.renderRawHTML)
}

func (r *escapedRawHTMLRenderer) renderHTMLBlock(
	w util.BufWriter,
	source []byte,
	node ast.Node,
	entering bool,
) (ast.WalkStatus, error) {
	n := node.(*ast.HTMLBlock)
	if entering {
		for i := range n.Lines().Len() {
			line := n.Lines().At(i)
			_, _ = w.Write(util.EscapeHTML(line.Value(source)))
		}
		return ast.WalkContinue, nil
	}
	if n.HasClosure() {
		closure := n.ClosureLine
		_, _ = w.Write(util.EscapeHTML(closure.Value(source)))
	}
	return ast.WalkContinue, nil
}

func (r *escapedRawHTMLRenderer) renderRawHTML(
	w util.BufWriter,
	source []byte,
	node ast.Node,
	entering bool,
) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkSkipChildren, nil
	}
	n := node.(*ast.RawHTML)
	for i := range n.Segments.Len() {
		segment := n.Segments.At(i)
		_, _ = w.Write(util.EscapeHTML(segment.Value(source)))
	}
	return ast.WalkSkipChildren, nil
}

var mdRenderer = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(gmhtml.WithHardWraps()),
	goldmark.WithRenderer(renderer.NewRenderer(
		renderer.WithNodeRenderers(
			util.Prioritized(gmhtml.NewRenderer(), 1000),
			util.Prioritized(&linkRelRenderer{}, 100),
			util.Prioritized(&escapedRawHTMLRenderer{}, 100),
		),
	)),
)

// renderMarkdown converts markdown s to safe HTML. Raw HTML is rendered as
// escaped text, never executable HTML. Empty input returns a placeholder element.
func renderMarkdown(s string) template.HTML {
	if strings.TrimSpace(s) == "" {
		return `<em class="muted">(empty)</em>`
	}
	var buf bytes.Buffer
	if err := mdRenderer.Convert([]byte(s), &buf); err != nil {
		return template.HTML(html.EscapeString(s))
	}
	//nolint:gosec // raw HTML nodes are escaped by escapedRawHTMLRenderer
	return template.HTML(buf.String())
}
