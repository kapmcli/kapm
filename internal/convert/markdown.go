package convert

import (
	"path/filepath"
	"strings"

	"github.com/kapmcli/kapm/internal/frontmatter"
	"github.com/kapmcli/kapm/internal/paths"
)

// ConvertPromptsWithReport converts prompts and reports converted or skipped files.
func ConvertPromptsWithReport(srcDir, dstDir string, force bool) (Report, error) {
	return convertDocumentsWithReport(srcDir, "prompts", "*.prompt.md", "prompts", force, func(path string, doc frontmatter.Document) (documentWriteTarget, error) {
		name := strings.TrimSuffix(filepath.Base(path), ".prompt.md")
		return documentWriteTarget{
			path: filepath.Join(dstDir, paths.PromptsSubdir, name+".md"),
			data: []byte(bodyWithoutLeadingBlankLine(doc.Body)),
		}, nil
	})
}

// ConvertInstructionsWithReport converts instructions and reports converted or skipped files.
func ConvertInstructionsWithReport(srcDir, dstDir string, force bool) (Report, error) {
	return convertDocumentsWithReport(srcDir, "instructions", "*.instructions.md", "instructions", force, func(path string, doc frontmatter.Document) (documentWriteTarget, error) {
		name := strings.TrimSuffix(filepath.Base(path), ".instructions.md")
		return documentWriteTarget{
			path: filepath.Join(dstDir, paths.SteeringSubdir, name+".md"),
			data: []byte("---\ninclusion: always\n---\n\n" + bodyWithoutLeadingBlankLine(doc.Body)),
		}, nil
	})
}

// ConvertCommandsWithReport converts commands and reports converted or skipped files.
func ConvertCommandsWithReport(srcDir, dstDir string, force bool) (Report, error) {
	return convertDocumentsWithReport(srcDir, "commands", "*.md", "commands", force, func(path string, doc frontmatter.Document) (documentWriteTarget, error) {
		return documentWriteTarget{
			path: filepath.Join(dstDir, paths.PromptsSubdir, filepath.Base(path)),
			data: []byte(bodyWithoutLeadingBlankLine(doc.Body)),
		}, nil
	})
}
