package convert

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kapmcli/kapm/internal/fileutil"
	"github.com/kapmcli/kapm/internal/frontmatter"
)

type documentWriteTarget struct {
	path       string
	data       []byte
	secondPath string
	secondData []byte
}

func primitiveFiles(srcDir, subdir, pattern string) ([]string, error) {
	dir := filepath.Join(srcDir, subdir)
	entries, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return nil, fmt.Errorf("convert glob %q: %w", dir, err)
	}
	slices.Sort(entries)
	return entries, nil
}

func readDocument(path string) (frontmatter.Document, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return frontmatter.Document{}, fmt.Errorf("convert read %q: %w", path, err)
	}

	doc, err := frontmatter.Parse(string(content))
	if err != nil {
		return frontmatter.Document{}, fmt.Errorf("convert parse %q: %w", path, err)
	}

	return doc, nil
}

func bodyWithoutLeadingBlankLine(body string) string {
	return strings.TrimLeft(body, "\r\n")
}

func convertDocumentsWithReport(srcDir, subdir, pattern, errorLabel string, force bool, render func(path string, doc frontmatter.Document) (documentWriteTarget, error)) (Report, error) {
	files, err := primitiveFiles(srcDir, subdir, pattern)
	if err != nil {
		return Report{}, err
	}

	report := Report{}
	for _, path := range files {
		doc, err := readDocument(path)
		if err != nil {
			return Report{}, err
		}

		target, err := render(path, doc)
		if err != nil {
			return Report{}, err
		}
		var written bool
		if target.secondPath != "" {
			for _, p := range []string{target.path, target.secondPath} {
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					return Report{}, wrapConvertError(errorLabel, path, err)
				}
			}
			written, err = fileutil.WriteFilePair(target.path, target.data, target.secondPath, target.secondData, force)
		} else {
			written, err = fileutil.WriteFileAtomic(target.path, target.data, force)
		}
		if err != nil {
			return Report{}, wrapConvertError(errorLabel, path, err)
		}
		if !written && !force {
			slog.Warn("kapm skip existing output", "path", target.path)
		}
		if written {
			report.Converted++
		} else {
			report.Skipped++
		}
	}

	return report, nil
}

func convertDirectoriesWithReport(names []string, converter func(name string) (bool, error)) (Report, error) {
	report := Report{}
	for _, name := range names {
		written, err := converter(name)
		if err != nil {
			return Report{}, err
		}
		if written {
			report.Converted++
		} else {
			report.Skipped++
		}
	}
	return report, nil
}
