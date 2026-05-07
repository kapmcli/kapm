package syncer

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"github.com/kapmcli/kapm/internal/convert"
	"github.com/kapmcli/kapm/internal/paths"
	"gopkg.in/yaml.v3"
)

// readFileFunc is the function used to read files; overridable in tests.
var readFileFunc = os.ReadFile

// Options controls a sync run.
type Options struct {
	Root  string
	Force bool
}

type projectManifest struct {
	Name         string `yaml:"name"`
	Version      string `yaml:"version"`
	Description  string `yaml:"description"`
	Author       string `yaml:"author"`
	Dependencies struct {
		APM []apmDependency         `yaml:"apm"`
		MCP []convert.MCPDependency `yaml:"mcp"`
	} `yaml:"dependencies"`
	Scripts map[string]string `yaml:"scripts"`
}

type apmDependency struct {
	Reference string `yaml:"-"`
	Git       string `yaml:"git"`
	Path      string `yaml:"path"`
	Alias     string `yaml:"alias"`
}

// UnmarshalYAML accepts either scalar dependency references or mapping values.
func (d *apmDependency) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		return node.Decode(&d.Reference)
	case yaml.MappingNode:
		type rawDependency apmDependency
		var raw rawDependency
		if err := node.Decode(&raw); err != nil {
			return fmt.Errorf("decode dependency: %w", err)
		}
		*d = apmDependency(raw)
		return nil
	default:
		return errors.New("unsupported dependency format")
	}
}

// Run converts local and installed APM primitives into .kiro output.
func Run(opts Options) error {
	root := opts.Root
	if root == "" {
		root = "."
	}

	manifest, err := loadManifest(filepath.Join(root, paths.APMManifest))
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}

	sources, err := sourceAPMDirs(root, opts.Force, manifest)
	if err != nil {
		return fmt.Errorf("source apm dirs: %w", err)
	}

	destination := filepath.Join(root, paths.KiroDir)
	total := convert.Report{}

	for _, src := range sources {
		report, err := runConverters(src, destination, opts.Force, manifest)
		if err != nil {
			return fmt.Errorf("run converters: %w", err)
		}
		total.Add(report)
	}

	slog.Info("kapm sync complete", "root", root, "sources", len(sources), "converted", total.Converted, "skipped", total.Skipped)
	return nil
}

// loadManifest reads and parses apm.yml at manifestPath.
// Returns a zero-value manifest (no error) if the file does not exist.
func loadManifest(manifestPath string) (projectManifest, error) {
	manifest, _, err := apmconfig.LoadStrictYAMLManifest[projectManifest](
		manifestPath,
		readFileFunc,
		func(path string, err error) error { return fmt.Errorf("sync stat %q: %w", path, err) },
		func(path string, err error) error { return fmt.Errorf("sync read %q: %w", path, err) },
	)
	return manifest, err
}

func sourceAPMDirs(root string, force bool, manifest projectManifest) ([]string, error) {
	moduleDirs, err := moduleAPMDirs(root, manifest)
	if err != nil {
		return nil, fmt.Errorf("sync source apm dirs: %w", err)
	}

	highPriority := make([]string, 0, len(moduleDirs)+1)
	localApm := filepath.Join(root, paths.APMSubdir)
	if info, err := os.Stat(localApm); err == nil && info.IsDir() {
		highPriority = append(highPriority, localApm)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("sync stat %q: %w", localApm, err)
	}
	highPriority = append(highPriority, moduleDirs...)

	if force {
		slices.Reverse(highPriority)
	}

	return highPriority, nil
}

// primitiveDirNames lists directory names that qualify a package root as an
// APM source even without an apm.yml or .apm/ subdir (virtual-package layout
// produced by `apm install <org>/<repo>/skills/<name>` and similar).
var primitiveDirNames = [...]string{
	"skills", "prompts", "instructions", "agents", "chatmodes", "commands",
}

func hasPrimitiveDir(dir string) bool {
	for _, name := range primitiveDirNames {
		info, err := os.Stat(filepath.Join(dir, name))
		if err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// pickSourceDir returns the source directory for a package root: prefer
// <packageRoot>/.apm when it exists, else the package root itself if any
// primitive subdir is present. Returns "" if nothing to sync.
func pickSourceDir(packageRoot string) string {
	apmDir := filepath.Join(packageRoot, paths.APMSubdir)
	if info, err := os.Stat(apmDir); err == nil && info.IsDir() {
		return apmDir
	}
	if hasPrimitiveDir(packageRoot) {
		return packageRoot
	}
	return ""
}

func moduleAPMDirs(root string, manifest projectManifest) ([]string, error) {
	modulesRoot := filepath.Join(root, paths.APMModulesDir)
	if _, err := os.Stat(modulesRoot); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("sync stat %q: %w", modulesRoot, err)
	}

	packageSources, err := discoverPackageSources(modulesRoot)
	if err != nil {
		return nil, fmt.Errorf("discover package roots: %w", err)
	}
	if len(packageSources) == 0 {
		return nil, nil
	}

	orderedSources, err := orderedModuleSources(manifest, packageSources)
	if err != nil {
		return nil, fmt.Errorf("sync module apm dirs: %w", err)
	}
	return orderedSources, nil
}

// discoverPackageSources returns packageKey -> selected source dir for every
// apm_modules/<org>/<repo>/ directory that looks like an APM package.
func discoverPackageSources(modulesRoot string) (map[string]string, error) {
	orgs, err := os.ReadDir(modulesRoot)
	if err != nil {
		return nil, fmt.Errorf("sync read %q: %w", modulesRoot, err)
	}
	out := make(map[string]string)
	for _, org := range orgs {
		if !org.IsDir() {
			continue
		}
		orgPath := filepath.Join(modulesRoot, org.Name())
		repos, err := os.ReadDir(orgPath)
		if err != nil {
			return nil, fmt.Errorf("sync read %q: %w", orgPath, err)
		}
		for _, repo := range repos {
			if !repo.IsDir() {
				continue
			}
			packageRoot := filepath.Join(orgPath, repo.Name())
			sourceDir := pickSourceDir(packageRoot)
			if sourceDir == "" {
				continue
			}
			key := toSlashJoin(org.Name(), repo.Name())
			out[key] = sourceDir
		}
	}
	return out, nil
}

func orderedModuleSources(manifest projectManifest, packageSources map[string]string) ([]string, error) {
	orderedKeys := slices.Sorted(maps.Keys(packageSources))

	candidates := make([][]string, 0, len(manifest.Dependencies.APM))
	for _, dep := range manifest.Dependencies.APM {
		if paths := dep.moduleCandidates(); len(paths) > 0 {
			candidates = append(candidates, paths)
		}
	}

	ordered := make([]string, 0, len(packageSources))
	used := make(map[string]bool, len(packageSources))
	for _, cands := range candidates {
		key, ok := matchDependencyPackage(cands, orderedKeys, used)
		if !ok {
			continue
		}
		ordered = append(ordered, packageSources[key])
		used[key] = true
	}

	for _, key := range orderedKeys {
		if used[key] {
			continue
		}
		ordered = append(ordered, packageSources[key])
	}

	return ordered, nil
}

func matchDependencyPackage(candidates []string, orderedKeys []string, used map[string]bool) (string, bool) {
	for _, candidate := range candidates {
		for _, key := range orderedKeys {
			if used[key] {
				continue
			}
			if key == candidate {
				return key, true
			}
		}
	}

	for _, candidate := range candidates {
		for _, key := range orderedKeys {
			if used[key] {
				continue
			}
			if strings.HasSuffix(key, "/"+candidate) {
				return key, true
			}
		}
	}

	return "", false
}

func toSlashJoin(parts ...string) string { return filepath.ToSlash(filepath.Join(parts...)) }

func (d apmDependency) moduleCandidates() []string {
	if ref := strings.TrimSpace(d.Reference); ref != "" {
		if candidate, ok := normalizeDependencyReference(ref); ok {
			return []string{candidate}
		}
		return nil
	}

	if localPath := strings.TrimSpace(d.Path); localPath != "" && strings.TrimSpace(d.Git) == "" {
		key, ok := localDependencyKey(localPath)
		if !ok {
			return nil
		}
		return []string{key}
	}

	gitRef := strings.TrimSpace(d.Git)
	if gitRef == "" {
		return nil
	}
	repoPath, ok := normalizeDependencyReference(gitRef)
	if !ok {
		return nil
	}

	candidates := make([]string, 0, 4)
	if alias := strings.TrimSpace(d.Alias); alias != "" {
		candidates = append(candidates,
			filepath.ToSlash(alias),
			toSlashJoin(filepath.Dir(repoPath), alias),
		)
	}
	if virtualPath := strings.Trim(strings.TrimSpace(d.Path), "/"); virtualPath != "" {
		candidates = append(candidates, toSlashJoin(repoPath, filepath.FromSlash(virtualPath)))
	}
	candidates = append(candidates, repoPath)
	return dedupeStrings(candidates)
}

func normalizeDependencyReference(ref string) (string, bool) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "", false
	}

	if hash := strings.Index(trimmed, "#"); hash >= 0 {
		trimmed = trimmed[:hash]
	}
	trimmed = strings.TrimSuffix(trimmed, ".git")

	switch {
	case strings.HasPrefix(trimmed, "./"), strings.HasPrefix(trimmed, "../"), strings.HasPrefix(trimmed, "/"), strings.HasPrefix(trimmed, "~/"):
		return localDependencyKey(trimmed)
	case strings.HasPrefix(trimmed, "https://"), strings.HasPrefix(trimmed, "http://"), strings.HasPrefix(trimmed, "ssh://"):
		trimmed = stripURLScheme(trimmed)
	case strings.HasPrefix(trimmed, "git@"):
		trimmed = strings.TrimPrefix(trimmed, "git@")
		trimmed = strings.Replace(trimmed, ":", "/", 1)
	}

	trimmed = strings.TrimPrefix(trimmed, "github.com/")
	trimmed = strings.TrimPrefix(trimmed, "/")
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		return "", false
	}

	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(trimmed))), true
}

func localDependencyKey(path string) (string, bool) {
	base := filepath.Base(filepath.Clean(path))
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "", false
	}
	return toSlashJoin("_local", base), true
}

func stripURLScheme(value string) string {
	withoutScheme := value
	if _, after, ok := strings.Cut(withoutScheme, "://"); ok {
		withoutScheme = after
	}
	withoutScheme = strings.TrimPrefix(withoutScheme, "git@")
	return withoutScheme
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func runConverters(srcDir, dstDir string, force bool, manifest projectManifest) (convert.Report, error) {
	total := convert.Report{}
	steps := []struct {
		name string
		run  func(string, string, bool) (convert.Report, error)
	}{
		{name: "instructions", run: convert.ConvertInstructionsWithReport},
		{name: "prompts", run: convert.ConvertPromptsWithReport},
		{name: "commands", run: convert.ConvertCommandsWithReport},
		{name: "skills", run: convert.ConvertSkillsWithReport},
		{name: "agents", run: convert.ConvertAgentsWithReport},
	}

	for _, step := range steps {
		report, err := step.run(srcDir, dstDir, force)
		if err != nil {
			return convert.Report{}, fmt.Errorf("sync %s from %q: %w", step.name, srcDir, err)
		}
		total.Add(report)
	}

	report, err := convert.ConvertMCPWithDeps(manifest.Dependencies.MCP, srcDir, dstDir, force)
	if err != nil {
		return convert.Report{}, fmt.Errorf("sync mcp from %q: %w", srcDir, err)
	}
	total.Add(report)

	return total, nil
}
