package stylegen

import (
	"bytes"
	"log/slog"
	"regexp"
	"strings"
	"testing"
)

const happyDesign = `---
name: kapm
colors:
  bg: "#111111"
  bg-2: "#222222"
  card: "#333333"
  accent: "#444444"
  success: "#555555"
  error: "#666666"
  warning: "#777777"
  text: "#888888"
  muted: "#999999"
  chart: "#aaaaaa"
---

## Overview
prose body
`

func TestParseDesignMD_HappyPath(t *testing.T) {
	d, err := ParseDesignMD([]byte(happyDesign))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Name != "kapm" {
		t.Errorf("name = %q, want kapm", d.Name)
	}
	for _, k := range ColorKeys {
		if _, ok := d.Colors[k]; !ok {
			t.Errorf("missing color %q", k)
		}
	}
	if d.Colors["bg"] != "#111111" {
		t.Errorf("bg = %q, want #111111", d.Colors["bg"])
	}
}

func TestParseDesignMD_MissingColors(t *testing.T) {
	cases := map[string]string{
		"no section": `---
name: kapm
---

body
`,
		"missing key": `---
name: kapm
colors:
  bg: "#111111"
  bg-2: "#222222"
  card: "#333333"
  accent: "#444444"
  success: "#555555"
  error: "#666666"
  warning: "#777777"
  text: "#888888"
  muted: "#999999"
---

body
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDesignMD([]byte(src)); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestParseDesignMD_MalformedYAML(t *testing.T) {
	cases := map[string]string{
		"no front matter": "# Just markdown\n\nno front matter here\n",
		"broken yaml": `---
name: kapm
colors:
  bg: "#111111
  - : not a map
---
body
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDesignMD([]byte(src)); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

const sampleCSS = `:root {
  --bg:      #000000;
  --bg-2:    #000000;
  --card:    #000000;
  --accent:  #000000;
  --success: #000000;
  --error:   #000000;
  --warning: #000000;
  --chart:   #000000;
  --text:    #000000;
  --muted:   #000000;
}

body { background: var(--bg); color: var(--text); }

.card { background: var(--card); }

.badge-success { color: var(--success); }
`

func TestGenerateStyleCSS_Preservation(t *testing.T) {
	d, err := ParseDesignMD([]byte(happyDesign))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := GenerateStyleCSS([]byte(sampleCSS), d)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	outStr := string(out)
	for _, want := range []string{
		"body { background: var(--bg); color: var(--text); }",
		".card { background: var(--card); }",
		".badge-success { color: var(--success); }",
	} {
		if !strings.Contains(outStr, want) {
			t.Errorf("output missing preserved rule %q", want)
		}
	}
	newVarRE := regexp.MustCompile(`--bg:\s+#111111;|--accent:\s+#444444;|--chart:\s+#aaaaaa;`)
	if m := newVarRE.FindAllString(outStr, -1); len(m) != 3 {
		t.Errorf("expected 3 new var declarations, got %d in:\n%s", len(m), outStr)
	}
	if strings.Contains(outStr, "#000000") {
		t.Errorf("output still contains old color #000000:\n%s", outStr)
	}
	out2, err := GenerateStyleCSS(out, d)
	if err != nil {
		t.Fatalf("generate2: %v", err)
	}
	if !bytes.Equal(out, out2) {
		t.Errorf("not idempotent")
	}
}

const unknownDesign = `---
name: kapm
colors:
  bg: "#111111"
  bg-2: "#222222"
  card: "#333333"
  accent: "#444444"
  success: "#555555"
  error: "#666666"
  warning: "#777777"
  text: "#888888"
  muted: "#999999"
  chart: "#aaaaaa"
  extra: "#ff00ff"
typography:
  body: system-ui
rounded:
  sm: "4px"
spacing:
  base: "8px"
---
body
`

func TestGenerateStyleCSS_UnknownTokens(t *testing.T) {
	d, err := ParseDesignMD([]byte(unknownDesign))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(orig)

	out, err := GenerateStyleCSS([]byte(sampleCSS), d)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	outStr := string(out)
	if strings.Contains(outStr, "--extra") {
		t.Errorf("unknown token --extra leaked to CSS:\n%s", outStr)
	}
	if strings.Contains(outStr, "#ff00ff") {
		t.Errorf("unknown token color leaked to CSS:\n%s", outStr)
	}
	if !strings.Contains(buf.String(), `key=extra`) {
		t.Errorf("no slog warning for unknown token, got: %q", buf.String())
	}
	for _, forbidden := range []string{"font-family", "system-ui", "border-radius", "4px", "--sm"} {
		if strings.Contains(outStr, forbidden) {
			t.Errorf("non-color token %q leaked to CSS:\n%s", forbidden, outStr)
		}
	}
	if d.Typography["body"] != "system-ui" {
		t.Errorf("typography not parsed: %+v", d.Typography)
	}
	if d.Rounded["sm"] != "4px" {
		t.Errorf("rounded not parsed: %+v", d.Rounded)
	}
}
