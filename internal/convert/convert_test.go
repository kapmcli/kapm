package convert_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kapmcli/kapm/internal/convert"
	"github.com/kapmcli/kapm/internal/testutil"
)

func TestConvertInstructions(t *testing.T) {
	t.Parallel()
	runConverterGoldenTest(t, filepath.Join(repoTestdataRoot(), "convert", "instructions", "input"), filepath.Join(repoTestdataRoot(), "convert", "instructions", "expected"), convert.ConvertInstructionsWithReport)
}

func TestConvertInstructionsSkipExisting(t *testing.T) {
	t.Parallel()

	src := filepath.Join(repoTestdataRoot(), "convert", "instructions", "input")
	dst := t.TempDir()
	existingPath := filepath.Join(dst, "steering", "design-standards.md")
	if err := os.MkdirAll(filepath.Dir(existingPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	const existing = "existing\n"
	if err := os.WriteFile(existingPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	if _, err := convert.ConvertInstructionsWithReport(src, dst, false); err != nil {
		t.Fatalf("ConvertInstructions() error = %v", err)
	}

	got, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}
	if string(got) != existing {
		t.Fatalf("existing file overwritten: got %q, want %q", got, existing)
	}
}

func TestConvertInstructionsForce(t *testing.T) {
	t.Parallel()

	src := filepath.Join(repoTestdataRoot(), "convert", "instructions", "input")
	dst := t.TempDir()
	existingPath := filepath.Join(dst, "steering", "design-standards.md")
	if err := os.MkdirAll(filepath.Dir(existingPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	if err := os.WriteFile(existingPath, []byte("existing\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	if _, err := convert.ConvertInstructionsWithReport(src, dst, true); err != nil {
		t.Fatalf("ConvertInstructions() error = %v", err)
	}

	testutil.AssertDirEqual(t, dst, filepath.Join(repoTestdataRoot(), "convert", "instructions", "expected"))
}

func TestConvertPrompts(t *testing.T) {
	t.Parallel()
	runConverterGoldenTest(t, filepath.Join(repoTestdataRoot(), "convert", "prompts", "input"), filepath.Join(repoTestdataRoot(), "convert", "prompts", "expected"), convert.ConvertPromptsWithReport)
}

func TestConvertCommands(t *testing.T) {
	t.Parallel()
	runConverterGoldenTest(t, filepath.Join(repoTestdataRoot(), "convert", "commands", "input"), filepath.Join(repoTestdataRoot(), "convert", "commands", "expected"), convert.ConvertCommandsWithReport)
}

func TestConvertCommandsNoDir(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	dst := t.TempDir()
	if _, err := convert.ConvertCommandsWithReport(src, dst, false); err != nil {
		t.Fatalf("ConvertCommands() error = %v", err)
	}
}

func TestMarkdownConvertersWithReport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		run  func(string, string, bool) (convert.Report, error)
	}{
		{
			name: "instructions",
			src:  filepath.Join(repoTestdataRoot(), "convert", "instructions", "input"),
			run:  convert.ConvertInstructionsWithReport,
		},
		{
			name: "prompts",
			src:  filepath.Join(repoTestdataRoot(), "convert", "prompts", "input"),
			run:  convert.ConvertPromptsWithReport,
		},
		{
			name: "commands",
			src:  filepath.Join(repoTestdataRoot(), "convert", "commands", "input"),
			run:  convert.ConvertCommandsWithReport,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := t.TempDir()

			report, err := tt.run(tt.src, dst, false)
			if err != nil {
				t.Fatalf("first run error = %v", err)
			}
			if report.Converted == 0 {
				t.Fatalf("first run Converted = %d, want > 0", report.Converted)
			}
			if report.Skipped != 0 {
				t.Fatalf("first run Skipped = %d, want 0", report.Skipped)
			}

			report, err = tt.run(tt.src, dst, false)
			if err != nil {
				t.Fatalf("second run error = %v", err)
			}
			if report.Converted != 0 {
				t.Fatalf("second run Converted = %d, want 0", report.Converted)
			}
			if report.Skipped == 0 {
				t.Fatalf("second run Skipped = %d, want > 0", report.Skipped)
			}
		})
	}
}

func TestConvertSkills(t *testing.T) {
	t.Parallel()
	runConverterGoldenTest(t, filepath.Join(repoTestdataRoot(), "convert", "skills", "input"), filepath.Join(repoTestdataRoot(), "convert", "skills", "expected"), convert.ConvertSkillsWithReport)
}

func TestConvertAgentsDescriptionOnly(t *testing.T) {
	t.Parallel()
	runConverterGoldenTest(t, filepath.Join(repoTestdataRoot(), "convert", "agents-description-only", "input"), filepath.Join(repoTestdataRoot(), "convert", "agents-description-only", "expected"), convert.ConvertAgentsWithReport)
}

func TestConvertAgentsWithModel(t *testing.T) {
	t.Parallel()
	runConverterGoldenTest(t, filepath.Join(repoTestdataRoot(), "convert", "agents-with-model", "input"), filepath.Join(repoTestdataRoot(), "convert", "agents-with-model", "expected"), convert.ConvertAgentsWithReport)
}

func TestConvertAgentsChatmode(t *testing.T) {
	t.Parallel()
	runConverterGoldenTest(t, filepath.Join(repoTestdataRoot(), "convert", "agents-chatmode", "input"), filepath.Join(repoTestdataRoot(), "convert", "agents-chatmode", "expected"), convert.ConvertAgentsWithReport)
}

func TestConvertAgentsRejectsInvalidName(t *testing.T) {
	t.Parallel()

	src := filepath.Join(repoTestdataRoot(), "convert", "agents-invalid-name", "input")
	dst := t.TempDir()
	_, err := convert.ConvertAgentsWithReport(src, dst, false)
	if err == nil {
		t.Fatal("ConvertAgents() error = nil, want invalid name error")
	}
}

func runConverterGoldenTest(t *testing.T, srcFixture, expectedFixture string, run func(string, string, bool) (convert.Report, error)) {
	t.Helper()

	dst := t.TempDir()
	if _, err := run(srcFixture, dst, false); err != nil {
		t.Fatalf("converter error = %v", err)
	}
	testutil.AssertDirEqual(t, dst, expectedFixture)
}

func repoTestdataRoot() string {
	return filepath.Join("..", "..", "testdata")
}

func writeFileForTest(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
