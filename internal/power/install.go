package power

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"github.com/kapmcli/kapm/internal/convert"
	"github.com/kapmcli/kapm/internal/fileutil"
	"github.com/kapmcli/kapm/internal/frontmatter"
	"github.com/kapmcli/kapm/internal/paths"
)

var ErrSkipExisting = errors.New("power already exists")

var (
	newLocalFetcher = func() Fetcher { return localFetcher{} }
	newGitFetcher   = func() Fetcher { return gitFetcher{} }
)

type PowerManifest struct {
	Name        string
	Description string
	Path        string
	Body        string
}

// Install fetches a Power package and stores it in .kiro/powers/<name>/.
func Install(ctx context.Context, opts InstallOptions) (*Result, error) {
	opts = resolveInstallOptions(opts)

	tempDir, commit, cleanup, err := fetchPowerPackage(ctx, opts)
	defer cleanup()
	if err != nil {
		return nil, err
	}

	manifest, hasMCP, hasHooks, warnings, err := validateManifest(tempDir)
	if err != nil {
		return nil, err
	}

	installedDir, skipped, err := installPowerPackage(tempDir, manifest, opts)
	if err != nil {
		return nil, err
	}

	resourcePaths, err := listInstalledResourcePaths(installedDir)
	if err != nil {
		return nil, err
	}
	if skipped {
		return &Result{
			Name:           manifest.Name,
			PowerDir:       installedDir,
			ResourcePaths:  resourcePaths,
			MCPConfigPath:  resultMCPPath(opts.TargetDir, manifest.Name, hasMCP),
			HooksDir:       resultHooksDir(opts.TargetDir, manifest.Name, hasHooks),
			ResolvedCommit: commit,
			Skipped:        true,
		}, nil
	}
	for _, w := range warnings {
		slog.Warn(w)
	}
	return &Result{
		Name:           manifest.Name,
		PowerDir:       installedDir,
		ResourcePaths:  resourcePaths,
		MCPConfigPath:  resultMCPPath(opts.TargetDir, manifest.Name, hasMCP),
		HooksDir:       resultHooksDir(opts.TargetDir, manifest.Name, hasHooks),
		ResolvedCommit: commit,
		Warnings:       warnings,
		Skipped:        false,
	}, nil
}

func resolveInstallOptions(opts InstallOptions) InstallOptions {
	if opts.TargetDir == "" {
		opts.TargetDir = "."
	}
	if opts.Timeout == 0 {
		opts.Timeout = DefaultTimeout
	}
	return opts
}

func fetchPowerPackage(ctx context.Context, opts InstallOptions) (tempDir, commit string, cleanup func(), err error) {
	cleanup = func() {}
	fetcher, err := fetcherForSource(opts.Source)
	if err != nil {
		return "", "", cleanup, err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	slog.Info("fetching power", "source", sourceLabel(opts.Source))
	localDir, resolvedCommit, rawCleanup, fetchErr := fetcher.Fetch(fetchCtx, opts.Source)
	cleanup = func() {
		cancel()
		if rawCleanup != nil {
			rawCleanup()
		}
	}
	if fetchErr != nil {
		return "", "", cleanup, fmt.Errorf("fetch power: %w", fetchErr)
	}
	return localDir, resolvedCommit, cleanup, nil
}

func validateManifest(tempDir string) (*PowerManifest, bool, bool, []string, error) {
	manifest, err := readPower(tempDir)
	if err != nil {
		return nil, false, false, nil, err
	}
	_, warnings, err := loadPowerSteering(tempDir)
	if err != nil {
		return nil, false, false, nil, err
	}
	hasMCP, err := hasPowerMCP(tempDir)
	if err != nil {
		return nil, false, false, nil, err
	}
	hasHooks, err := hasPowerHooks(tempDir)
	if err != nil {
		return nil, false, false, nil, err
	}
	return manifest, hasMCP, hasHooks, warnings, nil
}

// installPowerPackage writes the package to disk and returns (powerDir, skipped, error).
func installPowerPackage(tempDir string, manifest *PowerManifest, opts InstallOptions) (string, bool, error) {
	powerDir := PowerDir(opts.TargetDir, manifest.Name)
	slog.Info("installing power", "name", manifest.Name, "powerDir", powerDir)
	err := writePowerFiles(opts.TargetDir, tempDir, manifest.Name, opts.Force)
	if errors.Is(err, ErrSkipExisting) {
		slog.Warn("skipped existing power install (use --force)", "powerDir", powerDir)
		return powerDir, true, nil
	}
	if err != nil {
		return "", false, err
	}
	return powerDir, false, nil
}

func readFileNoFollow(path string) ([]byte, error) {
	isLink, err := fileutil.IsSymlinkPath(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("lstat %q: %w", path, err)
	}
	if isLink {
		return nil, fmt.Errorf("refusing to read symlink: %s", path)
	}
	return os.ReadFile(path)
}

func readPower(dir string) (*PowerManifest, error) {
	path := filepath.Join(dir, "POWER.md")
	data, err := readFileNoFollow(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("POWER.md not found in %s", dir)
		}
		return nil, fmt.Errorf("read %q: %w", path, err)
	}

	doc, err := frontmatter.Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", path, err)
	}

	nameValue, _ := stringMetaField(doc.Meta, "name")
	name, err := apmconfig.ValidateIdentifier(nameValue)
	if err != nil {
		return nil, errors.New("POWER.md frontmatter must have a valid name")
	}

	description, _ := stringMetaField(doc.Meta, "description")
	if strings.TrimSpace(description) == "" {
		description, _ = stringMetaField(doc.Meta, "displayName")
	}
	if strings.TrimSpace(description) == "" {
		return nil, errors.New("POWER.md frontmatter must have description or displayName")
	}

	return &PowerManifest{
		Name:        name,
		Description: description,
		Path:        path,
		Body:        doc.Body,
	}, nil
}

func writePowerFiles(targetDir, srcDir, powerName string, force bool) error {
	powerDir := PowerDir(targetDir, powerName)
	if exists, err := pathExists(powerDir); err != nil {
		return fmt.Errorf("stat %q: %w", powerDir, err)
	} else if exists {
		powerManaged, err := isManagedPowerDir(powerDir, powerName)
		if err != nil {
			return err
		}
		if !powerManaged {
			return fmt.Errorf("%s exists and is not kapm power-managed; refusing to overwrite", powerDir)
		}
		if !force {
			return ErrSkipExisting
		}
		if err := removeDirectoryIfExists(powerDir); err != nil {
			return err
		}
	}

	if err := convert.CopyDirectoryContents(srcDir, powerDir); err != nil {
		return fmt.Errorf("copy power package from %q: %w", srcDir, err)
	}
	return nil
}

type PowerSteeringDoc struct {
	Name string
	Body string
}

func loadPowerSteering(srcDir string) ([]PowerSteeringDoc, []string, error) {
	var warnings []string

	steeringDir := filepath.Join(srcDir, paths.SteeringSubdir)
	info, err := os.Stat(steeringDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, warnings, nil
		}
		return nil, nil, fmt.Errorf("stat %q: %w", steeringDir, err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("%s exists but is not a directory", steeringDir)
	}

	var docs []PowerSteeringDoc
	err = filepath.WalkDir(steeringDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %q: %w", path, walkErr)
		}
		if path == steeringDir {
			return nil
		}
		if fileutil.IsSymlinkMode(entry.Type()) {
			slog.Warn("kapm skip symlink", "path", path)
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(steeringDir, path)
		if err != nil {
			return fmt.Errorf("rel %q: %w", path, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %q: %w", path, err)
		}
		doc, err := frontmatter.Parse(string(data))
		if err != nil {
			return fmt.Errorf("parse %q: %w", path, err)
		}
		docs = append(docs, PowerSteeringDoc{
			Name: steeringDocName(rel),
			Body: strings.TrimLeft(doc.Body, "\r\n"),
		})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	slices.SortFunc(docs, func(a, b PowerSteeringDoc) int {
		return strings.Compare(a.Name, b.Name)
	})
	return docs, warnings, nil
}

func parseSourceMCP(powerSrcDir string) (convert.MCPConfig, string, error) {
	path := filepath.Join(powerSrcDir, paths.MCPFile)
	data, err := readFileNoFollow(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return convert.MCPConfig{}, path, nil
		}
		return convert.MCPConfig{}, path, fmt.Errorf("read %q: %w", path, err)
	}

	var config convert.MCPConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return convert.MCPConfig{}, path, fmt.Errorf("invalid mcp.json: %w", err)
	}
	if config.MCPServers == nil {
		config.MCPServers = map[string]convert.MCPServerEntry{}
	}
	return config, path, nil
}

func hasPowerMCP(powerSrcDir string) (bool, error) {
	config, _, err := parseSourceMCP(powerSrcDir)
	if err != nil {
		return false, err
	}
	return len(config.MCPServers) > 0, nil
}

func hasPowerHooks(powerSrcDir string) (bool, error) {
	path := filepath.Join(powerSrcDir, paths.HooksSubdir)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat %q: %w", path, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%s exists but is not a directory", path)
	}
	return true, nil
}

func listInstalledResourcePaths(powerDir string) ([]string, error) {
	resourcePaths := []string{filepath.ToSlash(filepath.Join(powerDir, "POWER.md"))}

	steeringDir := filepath.Join(powerDir, paths.SteeringSubdir)
	info, err := os.Stat(steeringDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return resourcePaths, nil
		}
		return nil, fmt.Errorf("stat %q: %w", steeringDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s exists but is not a directory", steeringDir)
	}

	var steeringPaths []string
	err = filepath.WalkDir(steeringDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %q: %w", path, walkErr)
		}
		if entry.IsDir() {
			return nil
		}
		if fileutil.IsSymlinkMode(entry.Type()) {
			return nil
		}
		steeringPaths = append(steeringPaths, filepath.ToSlash(path))
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(steeringPaths)
	return append(resourcePaths, steeringPaths...), nil
}

func isManagedPowerDir(powerDir, expectedName string) (bool, error) {
	manifest, err := readPower(powerDir)
	if err != nil {
		if strings.Contains(err.Error(), "POWER.md not found") {
			return false, nil
		}
		return false, err
	}
	return manifest.Name == expectedName, nil
}

func fetcherForSource(src PowerSource) (Fetcher, error) {
	switch src.Kind {
	case SourceLocal:
		return newLocalFetcher(), nil
	case SourceGitRoot, SourceGitHubSubdir:
		return newGitFetcher(), nil
	default:
		return nil, fmt.Errorf("unsupported power source kind %q", src.Kind)
	}
}

func sourceLabel(src PowerSource) string {
	switch src.Kind {
	case SourceLocal:
		return src.Path
	default:
		return src.URL
	}
}

func stringMetaField(meta map[string]any, key string) (string, bool) {
	value, ok := meta[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(text), true
}

func steeringDocName(relativePath string) string {
	normalized := filepath.ToSlash(relativePath)
	ext := filepath.Ext(normalized)
	stem := strings.TrimSuffix(normalized, ext)
	stem = strings.ReplaceAll(stem, "/", "--")
	return stem
}

func resultMCPPath(targetDir, powerName string, hasMCP bool) string {
	if !hasMCP {
		return ""
	}
	return PowerMCPPath(targetDir, powerName)
}

func resultHooksDir(targetDir, powerName string, hasHooks bool) string {
	if !hasHooks {
		return ""
	}
	return PowerHooksDir(targetDir, powerName)
}

func removeDirectoryIfExists(path string) error {
	isLink, err := fileutil.IsSymlinkPath(path)
	if err == nil && isLink {
		return fmt.Errorf("remove %q: refuse to remove symlink", path)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %q: %w", path, err)
	}
	if err := os.RemoveAll(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %q: %w", path, err)
	}
	return nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}
