// Package stylegen parses DESIGN.md YAML front matter and renders a CSS
// :root block from the declared color tokens. It is shared by cmd/stylegen
// and by the /design-preview HTTP handler.
package stylegen

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Design is the parsed DESIGN.md front matter. v1 only consumes Colors; the
// remaining sections are accepted for forward compatibility.
type Design struct {
	Name        string            `yaml:"name"`
	Version     string            `yaml:"version"`
	Description string            `yaml:"description"`
	Colors      map[string]string `yaml:"colors"`
	Typography  map[string]any    `yaml:"typography"`
	Rounded     map[string]string `yaml:"rounded"`
	Spacing     map[string]any    `yaml:"spacing"`
	Components  map[string]any    `yaml:"components"`
}

// ColorKeys is the fixed v1 set of color tokens. Order defines CSS output
// order and the swatch order on /design-preview.
var ColorKeys = []string{
	"bg", "bg-2", "card", "accent",
	"success", "error", "warning",
	"chart", "text", "muted",
}

var (
	frontMatterRE = regexp.MustCompile(`(?s)\A---\s*\n(.*?)\n---\s*(?:\n|$)`)
	rootBlockRE   = regexp.MustCompile(`(?s):root\s*\{.*?\}`)
)

// ParseDesignMD extracts and decodes the YAML front matter from DESIGN.md
// content. It returns an error if the front matter is missing, malformed, or
// lacks required color keys.
func ParseDesignMD(src []byte) (*Design, error) {
	m := frontMatterRE.FindSubmatch(src)
	if m == nil {
		return nil, errors.New("design parse: missing YAML front matter")
	}
	var d Design
	if err := yaml.Unmarshal(m[1], &d); err != nil {
		return nil, fmt.Errorf("design parse: %w", err)
	}
	if len(d.Colors) == 0 {
		return nil, errors.New("design parse: colors section missing or empty")
	}
	for _, k := range ColorKeys {
		if _, ok := d.Colors[k]; !ok {
			return nil, fmt.Errorf("design parse: missing required color %q", k)
		}
	}
	return &d, nil
}

// GenerateRootBlock renders the :root { ... } CSS block from d.Colors.
// Unknown color keys emit a stderr warning and are ignored.
func GenerateRootBlock(d *Design) string {
	for k := range d.Colors {
		if !slices.Contains(ColorKeys, k) {
			slog.Warn("stylegen: unknown color key ignored", "key", k)
		}
	}
	var b strings.Builder
	b.WriteString(":root {\n")
	width := 0
	for _, k := range ColorKeys {
		if n := len(k); n > width {
			width = n
		}
	}
	for _, k := range ColorKeys {
		fmt.Fprintf(&b, "  --%-*s %s;\n", width+1, k+":", d.Colors[k])
	}
	b.WriteString("}")
	return b.String()
}

// GenerateStyleCSS replaces the first :root { ... } block in css with a new
// block generated from d. Returns an error if no :root block is present.
func GenerateStyleCSS(css []byte, d *Design) ([]byte, error) {
	if !rootBlockRE.Match(css) {
		return nil, errors.New("style css: :root block not found")
	}
	block := GenerateRootBlock(d)
	return rootBlockRE.ReplaceAll(css, []byte(block)), nil
}
