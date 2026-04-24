package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

func TestValidateAndNormalizeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "valid", input: "valid-name", want: "valid-name"},
		{name: "trimmed", input: "  trimmed-name  ", want: "trimmed-name"},
		{name: "empty", input: "", wantErr: true},
		{name: "path escape", input: "../escape", wantErr: true},
		{name: "path separator", input: "nested/name", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateAndNormalizeName(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validateAndNormalizeName(%q) error = nil, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateAndNormalizeName(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("validateAndNormalizeName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDefaultToolIndices(t *testing.T) {
	t.Parallel()

	got := defaultToolIndices(apmconfig.DefaultAgentTools, []string{"fs_read", "glob"})
	want := []int{0, 5}
	if !bytes.Equal([]byte{byte(got[0]), byte(got[1])}, []byte{byte(want[0]), byte(want[1])}) {
		t.Fatalf("defaultToolIndices() = %#v, want %#v", got, want)
	}
}

func TestEnsureNonNil(t *testing.T) {
	t.Parallel()

	got := ensureNonNil(nil)
	if got == nil {
		t.Fatal("ensureNonNil(nil) = nil, want empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("len(ensureNonNil(nil)) = %d, want 0", len(got))
	}

	original := []string{"fs_read"}
	cloned := ensureNonNil(original)
	cloned[0] = "changed"
	if original[0] != "fs_read" {
		t.Fatalf("ensureNonNil() reused backing array: original = %#v", original)
	}
}

func TestWriteFilePairRejectsSymlinkedKiro(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	external := t.TempDir()
	kiroPath := filepath.Join(root, ".kiro")
	if err := os.Symlink(external, kiroPath); err != nil {
		t.Fatalf("Symlink(%q, %q): %v", external, kiroPath, err)
	}

	firstPath := filepath.Join(root, ".kiro", "agents", "test.json")
	secondPath := filepath.Join(root, ".kiro", "agent-prompts", "test.md")
	_, err := writeValidatedPair(root, firstPath, []byte("{}"), secondPath, []byte("# test"), false)
	if err == nil {
		t.Fatal("writeValidatedPair() error = nil, want symlink rejection")
	}
	if !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("writeValidatedPair() error = %v, want symlink rejection", err)
	}
}
