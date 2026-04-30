package convert

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/kapmcli/kapm/internal/paths"
)

// ConvertSkills copies APM skills into `.kiro/skills`.
func ConvertSkills(srcDir, dstDir string, force bool) error {
	_, err := ConvertSkillsWithReport(srcDir, dstDir, force)
	if err != nil {
		return fmt.Errorf("convert skills: %w", err)
	}
	return nil
}

// ConvertSkillsWithReport copies skills and reports converted or skipped directories.
func ConvertSkillsWithReport(srcDir, dstDir string, force bool) (Report, error) {
	skillsDir := filepath.Join(srcDir, paths.SkillsSubdir)
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Report{}, nil
		}
		return Report{}, fmt.Errorf("convert read dir %q: %w", skillsDir, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	slices.Sort(names)

	return convertDirectoriesWithReport(names, func(name string) (bool, error) {
		written, err := copyDirectory(filepath.Join(skillsDir, name), filepath.Join(dstDir, paths.SkillsSubdir, name), force)
		if err != nil {
			return false, wrapConvertError("skills", name, err)
		}
		return written, nil
	})
}
