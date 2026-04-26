package convert

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"github.com/kapmcli/kapm/internal/fileutil"
	"github.com/kapmcli/kapm/internal/frontmatter"
	"gopkg.in/yaml.v3"
)

// Report captures converter output counts.
type Report struct {
	Converted int
	Skipped   int
}

// Add accumulates another converter report into r.
func (r *Report) Add(other Report) {
	r.Converted += other.Converted
	r.Skipped += other.Skipped
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

// wrapConvertError wraps err with a "convert <kind> <key>: " prefix.
func wrapConvertError(kind, key string, err error) error {
	return fmt.Errorf("convert %s %q: %w", kind, key, err)
}

type documentWriteTarget struct {
	path       string
	data       []byte
	secondPath string
	secondData []byte
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

func copyDirectory(src, dst string, force bool) (bool, error) {
	if force {
		if _, err := os.Lstat(src); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return false, nil
			}
			return false, fmt.Errorf("convert stat %q: %w", src, err)
		}
		if isLink, err := fileutil.IsSymlinkPath(dst); err == nil && isLink {
			return false, fmt.Errorf("convert remove %q: refuse to remove symlink", dst)
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("convert stat %q: %w", dst, err)
		}
		if err := os.RemoveAll(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("convert remove %q: %w", dst, err)
		}
	} else if _, err := os.Stat(dst); err == nil {
		slog.Warn("kapm skip existing output", "path", dst)
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("convert stat %q: %w", dst, err)
	}

	if err := copyDirectoryContents(src, dst); err != nil {
		return false, err
	}

	return true, nil
}

func copyDirectoryContents(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("convert read dir %q: %w", src, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("convert mkdir %q: %w", dst, err)
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if fileutil.IsSymlinkMode(entry.Type()) {
			slog.Warn("kapm skip symlink", "path", srcPath)
			continue
		}

		if entry.IsDir() {
			if err := copyDirectoryContents(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("convert stat %q: %w", srcPath, err)
		}

		if err := copyFile(srcPath, dstPath, info.Mode()); err != nil {
			return err
		}
	}

	return nil
}

// CopyDirectoryContents recursively copies src into dst while skipping symlinks
// with warnings. It preserves file modes up to 0o755.
func CopyDirectoryContents(src, dst string) error {
	return copyDirectoryContents(src, dst)
}

func copyFile(src, dst string, mode os.FileMode) (err error) {
	in, err := openNoFollow(src)
	if err != nil {
		return fmt.Errorf("convert open %q: %w", src, err)
	}
	defer func() {
		if closeErr := in.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("convert close %q: %w", src, closeErr)
		}
	}()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("convert mkdir %q: %w", filepath.Dir(dst), err)
	}

	tempFile, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return fmt.Errorf("convert create temp %q: %w", dst, err)
	}
	tempPath := tempFile.Name()
	defer func() {
		if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) && err == nil {
			err = fmt.Errorf("convert remove temp %q: %w", tempPath, removeErr)
		}
	}()

	if _, err := io.Copy(tempFile, in); err != nil {
		// best-effort cleanup; defer will remove the temp file regardless
		_ = tempFile.Close()
		return fmt.Errorf("convert copy %q: %w", dst, err)
	}
	if err := tempFile.Chmod(mode.Perm() & 0o755); err != nil {
		// best-effort cleanup; defer will remove the temp file regardless
		_ = tempFile.Close()
		return fmt.Errorf("convert chmod temp %q: %w", tempPath, err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("convert close temp %q: %w", tempPath, err)
	}
	if err := os.Rename(tempPath, dst); err != nil {
		return fmt.Errorf("convert rename %q: %w", dst, err)
	}
	return nil
}

// Metadata holds the typed frontmatter fields read by the convert package.
type Metadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Model       string `yaml:"model"`
}

// metadataFrom decodes a frontmatter map into a typed Metadata struct.
// Unknown fields are silently ignored.
func metadataFrom(meta map[string]any, path string) (Metadata, error) {
	raw, err := yaml.Marshal(meta)
	if err != nil {
		return Metadata{}, fmt.Errorf("convert parse %q: %w", path, err)
	}
	var m Metadata
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return Metadata{}, fmt.Errorf("convert parse %q: %w", path, err)
	}
	if strings.TrimSpace(m.Description) == "" {
		return Metadata{}, fmt.Errorf("convert parse %q: missing %q", path, "description")
	}
	return m, nil
}

func sanitizeIdentifier(value, field, path string) (string, error) {
	trimmed, err := apmconfig.ValidateIdentifier(value)
	if err != nil {
		return "", fmt.Errorf("convert parse %q: invalid %q", path, field)
	}
	return trimmed, nil
}
