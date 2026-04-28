package power

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePowerSource(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir() = %v", err)
	}

	tests := []struct {
		name          string
		input         string
		want          PowerSource
		wantErrSubstr string
	}{
		{
			name:  "local relative dot",
			input: "./local",
			want:  PowerSource{Kind: SourceLocal, Path: "./local"},
		},
		{
			name:  "local relative parent",
			input: "../local",
			want:  PowerSource{Kind: SourceLocal, Path: "../local"},
		},
		{
			name:  "local absolute",
			input: "/abs/local",
			want:  PowerSource{Kind: SourceLocal, Path: "/abs/local"},
		},
		{
			name:  "local home",
			input: "~/local",
			want:  PowerSource{Kind: SourceLocal, Path: filepath.Join(home, "local")},
		},
		{
			name:  "local home root",
			input: "~",
			want:  PowerSource{Kind: SourceLocal, Path: home},
		},
		{
			name:  "github root",
			input: "https://github.com/o/r",
			want:  PowerSource{Kind: SourceGitRoot, URL: "https://github.com/o/r"},
		},
		{
			name:  "github shorthand root",
			input: "o/r",
			want: PowerSource{
				Kind:  SourceGitRoot,
				URL:   "https://github.com/o/r",
				Owner: "o",
				Repo:  "r",
			},
		},
		{
			name:  "github shorthand subdir",
			input: "o/r/sub/dir",
			want: PowerSource{
				Kind:       SourceGitHubSubdir,
				URL:        "https://github.com/o/r",
				Owner:      "o",
				Repo:       "r",
				PathInRepo: "sub/dir",
			},
		},
		{
			name:  "github shorthand tree subdir",
			input: "upstash/context7/tree/master/plugins/context7-power",
			want: PowerSource{
				Kind:       SourceGitHubSubdir,
				URL:        "https://github.com/upstash/context7",
				Owner:      "upstash",
				Repo:       "context7",
				Ref:        "master",
				PathInRepo: "plugins/context7-power",
			},
		},
		{
			name:  "non-github http root",
			input: "https://example.com/o/r",
			want:  PowerSource{Kind: SourceGitRoot, URL: "https://example.com/o/r"},
		},
		{
			name:  "github tree missing ref falls back to root",
			input: "https://github.com/o/r/tree/",
			want:  PowerSource{Kind: SourceGitRoot, URL: "https://github.com/o/r/tree/"},
		},
		{
			name:  "github subdir root ref",
			input: "https://github.com/o/r/tree/main",
			want: PowerSource{
				Kind:       SourceGitHubSubdir,
				URL:        "https://github.com/o/r",
				Owner:      "o",
				Repo:       "r",
				Ref:        "main",
				PathInRepo: "",
			},
		},
		{
			name:  "github subdir root ref trailing slash",
			input: "https://github.com/o/r/tree/main/",
			want: PowerSource{
				Kind:       SourceGitHubSubdir,
				URL:        "https://github.com/o/r",
				Owner:      "o",
				Repo:       "r",
				Ref:        "main",
				PathInRepo: "",
			},
		},
		{
			name:  "github subdir nested",
			input: "https://github.com/o/r/tree/main/sub/dir",
			want: PowerSource{
				Kind:       SourceGitHubSubdir,
				URL:        "https://github.com/o/r",
				Owner:      "o",
				Repo:       "r",
				Ref:        "main",
				PathInRepo: "sub/dir",
			},
		},
		{
			name:  "github subdir nested trailing slash",
			input: "https://github.com/o/r/tree/main/sub/dir/",
			want: PowerSource{
				Kind:       SourceGitHubSubdir,
				URL:        "https://github.com/o/r",
				Owner:      "o",
				Repo:       "r",
				Ref:        "main",
				PathInRepo: "sub/dir",
			},
		},
		{
			name:  "github subdir non-greedy ref",
			input: "https://github.com/o/r/tree/feature/foo/sub",
			want: PowerSource{
				Kind:       SourceGitHubSubdir,
				URL:        "https://github.com/o/r",
				Owner:      "o",
				Repo:       "r",
				Ref:        "feature",
				PathInRepo: "foo/sub",
			},
		},
		{
			name:  "ssh git root",
			input: "git@github.com:o/r.git",
			want:  PowerSource{Kind: SourceGitRoot, URL: "git@github.com:o/r.git"},
		},
		{
			name:  "git protocol root",
			input: "git://example.com/r.git",
			want:  PowerSource{Kind: SourceGitRoot, URL: "git://example.com/r.git"},
		},
		{
			name:          "unsupported scheme rejected",
			input:         "ftp://example.com/r.git",
			wantErrSubstr: "unrecognized source",
		},
		{
			name:          "gitlab subdir rejected",
			input:         "https://gitlab.com/o/r/-/tree/main/sub",
			wantErrSubstr: "GitLab",
		},
		{
			name:          "codeberg subdir rejected",
			input:         "https://codeberg.org/o/r/src/branch/main/sub",
			wantErrSubstr: "Codeberg",
		},
		{
			name:          "empty string",
			input:         "",
			wantErrSubstr: "power source cannot be empty",
		},
		{
			name:  "plain string local path",
			input: "not a url at all",
			want:  PowerSource{Kind: SourceLocal, Path: "not a url at all"},
		},
		{
			name:  "local path with slash needs explicit dot",
			input: "./testdata/power/sample-power/input",
			want:  PowerSource{Kind: SourceLocal, Path: "./testdata/power/sample-power/input"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParsePowerSource(tc.input)
			if tc.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("ParsePowerSource(%q) error = nil, want substring %q", tc.input, tc.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("ParsePowerSource(%q) error = %q, want substring %q", tc.input, err.Error(), tc.wantErrSubstr)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParsePowerSource(%q) error = %v", tc.input, err)
			}
			if got.Kind != tc.want.Kind || got.Path != tc.want.Path || got.URL != tc.want.URL || got.Owner != tc.want.Owner || got.Repo != tc.want.Repo || got.Ref != tc.want.Ref || got.PathInRepo != tc.want.PathInRepo {
				t.Fatalf("ParsePowerSource(%q) = %#v, want %#v", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseGitHubURL_PathTraversal(t *testing.T) {
	cases := []string{
		"https://github.com/o/r/tree/main/../secret",
		"https://github.com/o/r/tree/main/sub/../etc",
		"https://github.com/o/r/tree/main/./sub",
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			_, ok := parseGitHubSubdirSource(input)
			if ok {
				t.Fatalf("parseGitHubSubdirSource(%q) = ok, want false", input)
			}
		})
	}
}

func TestParseGitHubShorthand_PathTraversal(t *testing.T) {
	cases := []string{
		"o/r/../secret",
		"o/r/sub/../etc",
		"o/r/./sub",
		"o/r/tree/main/../secret",
		"o/r/tree/main/sub/../etc",
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			_, ok := parseGitHubShorthandSource(input)
			if ok {
				t.Fatalf("parseGitHubShorthandSource(%q) = ok, want false", input)
			}
		})
	}
}

func TestParseGitHubURL_ValidSubdir(t *testing.T) {
	got, ok := parseGitHubSubdirSource("https://github.com/o/r/tree/main/sub/dir")
	if !ok {
		t.Fatal("parseGitHubSubdirSource valid subdir = false, want true")
	}
	if got.PathInRepo != "sub/dir" {
		t.Fatalf("PathInRepo = %q, want %q", got.PathInRepo, "sub/dir")
	}
}
