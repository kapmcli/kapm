package power

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/convert"
	"github.com/kapmcli/kapm/internal/syncer"
	"github.com/kapmcli/kapm/internal/testutil"
)

func TestReadPower(t *testing.T) {
	t.Run("sample power parses", func(t *testing.T) {
		manifest, err := readPower(filepath.Join(repoPowerFixtureRoot(), "sample-power", "input"))
		if err != nil {
			t.Fatalf("readPower() error = %v", err)
		}
		if manifest.Name != "sample-power" {
			t.Fatalf("manifest.Name = %q, want %q", manifest.Name, "sample-power")
		}
		if manifest.Description != "A sample power for testing." {
			t.Fatalf("manifest.Description = %q", manifest.Description)
		}
	})

	t.Run("displayName falls back to description", func(t *testing.T) {
		manifest, err := readPower(filepath.Join(repoPowerFixtureRoot(), "no-description", "input"))
		if err != nil {
			t.Fatalf("readPower() error = %v", err)
		}
		if manifest.Description != "No Description" {
			t.Fatalf("manifest.Description = %q, want %q", manifest.Description, "No Description")
		}
	})

	t.Run("missing description and displayName fails", func(t *testing.T) {
		root := t.TempDir()
		writeFileForTest(t, filepath.Join(root, "POWER.md"), []byte("---\nname: only-name\n---\n\nbody\n"))

		_, err := readPower(root)
		if err == nil || !strings.Contains(err.Error(), "description or displayName") {
			t.Fatalf("readPower() error = %v, want missing description/displayName", err)
		}
	})
}

func TestReadPower_SymlinkRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		if err := os.Symlink("nul", filepath.Join(t.TempDir(), "probe")); err != nil {
			t.Skip("symlinks require elevated privileges on Windows")
		}
	}
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	writeFileForTest(t, target, []byte("leak"))
	if err := os.Symlink(target, filepath.Join(root, "POWER.md")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	_, err := readPower(root)
	if err == nil || !strings.Contains(err.Error(), "refusing to read symlink") {
		t.Fatalf("readPower() error = %v, want refusing to read symlink", err)
	}
}

func TestParseSourceMCP_SymlinkRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		if err := os.Symlink("nul", filepath.Join(t.TempDir(), "probe")); err != nil {
			t.Skip("symlinks require elevated privileges on Windows")
		}
	}
	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	writeFileForTest(t, target, []byte(`{"mcpServers":{}}`))
	if err := os.Symlink(target, filepath.Join(root, "mcp.json")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	_, _, err := parseSourceMCP(root)
	if err == nil || !strings.Contains(err.Error(), "refusing to read symlink") {
		t.Fatalf("parseSourceMCP() error = %v, want refusing to read symlink", err)
	}
}

func TestListInstalledResourcePaths(t *testing.T) {
	powerDir := t.TempDir()
	writeFileForTest(t, filepath.Join(powerDir, "POWER.md"), []byte("---\nname: sample-power\ndescription: test\n---\n"))
	writeFileForTest(t, filepath.Join(powerDir, "steering", "b.md"), []byte("b\n"))
	writeFileForTest(t, filepath.Join(powerDir, "steering", "a.md"), []byte("a\n"))

	got, err := listInstalledResourcePaths(powerDir)
	if err != nil {
		t.Fatalf("listInstalledResourcePaths() error = %v", err)
	}

	want := []string{
		filepath.ToSlash(filepath.Join(powerDir, "POWER.md")),
		filepath.ToSlash(filepath.Join(powerDir, "steering", "a.md")),
		filepath.ToSlash(filepath.Join(powerDir, "steering", "b.md")),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resource paths = %#v, want %#v", got, want)
	}
}

func TestLoadPowerSteering(t *testing.T) {
	t.Run("missing steering is allowed", func(t *testing.T) {
		docs, warnings, err := loadPowerSteering(filepath.Join(repoPowerFixtureRoot(), "no-description", "input"))
		if err != nil {
			t.Fatalf("loadPowerSteering() error = %v", err)
		}
		if len(docs) != 0 {
			t.Fatalf("loadPowerSteering() docs = %d, want none", len(docs))
		}
		if len(warnings) != 0 {
			t.Fatalf("loadPowerSteering() warnings = %v, want none", warnings)
		}
	})

	t.Run("symlink in steering is skipped with warning", func(t *testing.T) {
		src := t.TempDir()
		steering := filepath.Join(src, "steering")
		if err := os.MkdirAll(steering, 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		writeFileForTest(t, filepath.Join(steering, "style.md"), []byte("# Style\n"))
		if err := os.Symlink(filepath.Join(steering, "style.md"), filepath.Join(steering, "link.md")); err != nil {
			t.Fatalf("Symlink() error = %v", err)
		}

		logs, restore := testutil.CaptureSlog(t)
		defer restore()

		docs, warnings, err := loadPowerSteering(src)
		if err != nil {
			t.Fatalf("loadPowerSteering() error = %v", err)
		}
		if len(warnings) != 0 {
			t.Fatalf("loadPowerSteering() warnings = %v, want none", warnings)
		}
		if len(docs) != 1 || docs[0].Name != "style" {
			t.Fatalf("loadPowerSteering() docs = %#v", docs)
		}
		if !strings.Contains(logs.String(), "skip symlink") {
			t.Fatalf("expected symlink warning, logs:\n%s", logs.String())
		}
	})
}

func TestInstall_LocalHappyPath(t *testing.T) {
	target := t.TempDir()
	sourcePath := filepath.Join(repoPowerFixtureRoot(), "sample-power", "input")

	result, err := Install(context.Background(), InstallOptions{
		Source:    PowerSource{Kind: SourceLocal, Path: sourcePath},
		TargetDir: target,
	})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	if result.Name != "sample-power" {
		t.Fatalf("result.Name = %q, want sample-power", result.Name)
	}
	if result.PowerDir != PowerDir(target, "sample-power") {
		t.Fatalf("result.PowerDir = %q", result.PowerDir)
	}
	if result.MCPConfigPath != PowerMCPPath(target, "sample-power") {
		t.Fatalf("result.MCPConfigPath = %q", result.MCPConfigPath)
	}
	if result.HooksDir != PowerHooksDir(target, "sample-power") {
		t.Fatalf("result.HooksDir = %q", result.HooksDir)
	}
	assertResourcePaths(t, result.ResourcePaths, []string{
		PowerDocPath(target, "sample-power"),
		filepath.Join(PowerDir(target, "sample-power"), "steering", "conventions.md"),
		filepath.Join(PowerDir(target, "sample-power"), "steering", "style.md"),
	})

	assertPowerPackageMatchesFixture(
		t,
		PowerDir(target, "sample-power"),
		filepath.Join(repoPowerFixtureRoot(), "sample-power", "expected", ".kiro", "powers", "sample-power"),
	)
	assertNoGeneratedSkill(t, PowerDir(target, "sample-power"))
}

func TestInstall_LocalGoldenPath(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
	}{
		{name: "sample power", fixture: "sample-power"},
		{name: "no mcp", fixture: "no-mcp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			sourcePath := filepath.Join(repoPowerFixtureRoot(), tt.fixture, "input")
			_, err := Install(context.Background(), InstallOptions{
				Source:    PowerSource{Kind: SourceLocal, Path: sourcePath},
				TargetDir: target,
			})
			if err != nil {
				t.Fatalf("Install() error = %v", err)
			}

			manifest := mustReadPower(t, sourcePath)
			assertResourcePaths(t, mustResourcePaths(t, PowerDir(target, manifest.Name)), expectedResourcePathsForFixture(target, manifest.Name, tt.fixture))

			assertPowerPackageMatchesFixture(
				t,
				PowerDir(target, manifest.Name),
				filepath.Join(repoPowerFixtureRoot(), tt.fixture, "expected", ".kiro", "powers", manifest.Name),
			)
			assertNoGeneratedSkill(t, PowerDir(target, manifest.Name))
		})
	}
}

func TestInstall_BadMCP_NoPartialWrites(t *testing.T) {
	target := t.TempDir()

	_, err := Install(context.Background(), InstallOptions{
		Source:    PowerSource{Kind: SourceLocal, Path: filepath.Join(repoPowerFixtureRoot(), "bad-mcp", "input")},
		TargetDir: target,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid mcp.json") {
		t.Fatalf("Install() error = %v, want invalid mcp.json", err)
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("target dir not empty after failed install: %v", entries)
	}
}

func TestInstall_DefaultSkipsExisting(t *testing.T) {
	target := t.TempDir()
	sourcePath := filepath.Join(repoPowerFixtureRoot(), "sample-power", "input")

	if _, err := Install(context.Background(), InstallOptions{
		Source:    PowerSource{Kind: SourceLocal, Path: sourcePath},
		TargetDir: target,
	}); err != nil {
		t.Fatalf("first Install() error = %v", err)
	}

	before := snapshotDir(t, filepath.Join(target, ".kiro"))
	if _, err := Install(context.Background(), InstallOptions{
		Source:    PowerSource{Kind: SourceLocal, Path: sourcePath},
		TargetDir: target,
	}); err != nil {
		t.Fatalf("second Install() error = %v", err)
	}
	after := snapshotDir(t, filepath.Join(target, ".kiro"))
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("default skip changed files\nbefore=%v\nafter=%v", before, after)
	}
}

func TestInstall_ForceOverwrites(t *testing.T) {
	target := t.TempDir()
	sourcePath := filepath.Join(repoPowerFixtureRoot(), "sample-power", "input")

	if _, err := Install(context.Background(), InstallOptions{
		Source:    PowerSource{Kind: SourceLocal, Path: sourcePath},
		TargetDir: target,
	}); err != nil {
		t.Fatalf("first Install() error = %v", err)
	}

	writeFileForTest(t, filepath.Join(PowerDir(target, "sample-power"), "POWER.md"), []byte("---\nname: sample-power\ndescription: mutated\n---\n\nmutated\n"))
	if _, err := Install(context.Background(), InstallOptions{
		Source:    PowerSource{Kind: SourceLocal, Path: sourcePath},
		TargetDir: target,
		Force:     true,
	}); err != nil {
		t.Fatalf("force Install() error = %v", err)
	}

	assertPowerPackageMatchesFixture(
		t,
		PowerDir(target, "sample-power"),
		filepath.Join(repoPowerFixtureRoot(), "sample-power", "expected", ".kiro", "powers", "sample-power"),
	)
}

func TestInstall_ForeignPowerDirRefused(t *testing.T) {
	testInstallForeignPowerDirRefused(t, true)
}

func TestInstall_ForeignPowerDirRefused_NoForce(t *testing.T) {
	testInstallForeignPowerDirRefused(t, false)
}

func TestInstall_MCPInvarianceVsKapmSync(t *testing.T) {
	root := t.TempDir()
	writeFileForTest(t, filepath.Join(root, "apm.yml"), []byte(strings.TrimSpace(`
name: workspace
version: 1.0.0
description: workspace test
dependencies:
  mcp:
    - name: existing-server
      url: https://example.com/mcp
`)+"\n"))
	if err := os.MkdirAll(filepath.Join(root, ".apm"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.apm): %v", err)
	}

	if err := syncer.Run(syncer.Options{Root: root}); err != nil {
		t.Fatalf("syncer.Run() error = %v", err)
	}
	config := readMCPJSON(t, filepath.Join(root, ".kiro", "settings", "mcp.json"))
	if _, ok := config.MCPServers["existing-server"]; !ok {
		t.Fatalf("missing existing-server after initial sync: %#v", config.MCPServers)
	}

	// sync --force should preserve installed power content and leave MCP config alone.
	if _, err := Install(context.Background(), InstallOptions{
		Source:    PowerSource{Kind: SourceLocal, Path: filepath.Join(repoPowerFixtureRoot(), "sample-power", "input")},
		TargetDir: root,
	}); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	beforePower := snapshotDir(t, PowerDir(root, "sample-power"))
	config = readMCPJSON(t, filepath.Join(root, ".kiro", "settings", "mcp.json"))
	if _, ok := config.MCPServers["power-sample-power-fetch"]; ok {
		t.Fatalf("power install should not merge MCP servers: %#v", config.MCPServers)
	}

	if err := syncer.Run(syncer.Options{Root: root, Force: true}); err != nil {
		t.Fatalf("syncer.Run(force) error = %v", err)
	}
	config = readMCPJSON(t, filepath.Join(root, ".kiro", "settings", "mcp.json"))
	for _, name := range []string{"existing-server", "power-sample-power-fetch", "power-sample-power-search"} {
		if name == "existing-server" {
			if _, ok := config.MCPServers[name]; !ok {
				t.Fatalf("missing %s after sync --force: %#v", name, config.MCPServers)
			}
			continue
		}
		if _, ok := config.MCPServers[name]; ok {
			t.Fatalf("unexpected %s after sync --force: %#v", name, config.MCPServers)
		}
	}
	afterPower := snapshotDir(t, PowerDir(root, "sample-power"))
	if !reflect.DeepEqual(beforePower, afterPower) {
		t.Fatalf("power install output changed after sync --force")
	}

	if _, err := Install(context.Background(), InstallOptions{
		Source:    PowerSource{Kind: SourceLocal, Path: filepath.Join(repoPowerFixtureRoot(), "sample-power", "input")},
		TargetDir: root,
		Force:     true,
	}); err != nil {
		t.Fatalf("force Install() error = %v", err)
	}
	config = readMCPJSON(t, filepath.Join(root, ".kiro", "settings", "mcp.json"))
	if _, ok := config.MCPServers["existing-server"]; !ok {
		t.Fatalf("existing-server missing after force install: %#v", config.MCPServers)
	}
	if _, ok := config.MCPServers["power-sample-power-fetch"]; ok {
		t.Fatalf("power install should not merge MCP servers on force: %#v", config.MCPServers)
	}
}

func TestInstall_NoPowerMD(t *testing.T) {
	target := t.TempDir()
	_, err := Install(context.Background(), InstallOptions{
		Source:    PowerSource{Kind: SourceLocal, Path: filepath.Join(repoPowerFixtureRoot(), "no-power-md", "input")},
		TargetDir: target,
	})
	if err == nil || !strings.Contains(err.Error(), "power.md") {
		t.Fatalf("Install() error = %v, want power.md error", err)
	}
	assertDirEmpty(t, target)
}

func TestInstall_NoDescriptionNoDisplayName(t *testing.T) {
	target := t.TempDir()
	src := t.TempDir()
	writeFileForTest(t, filepath.Join(src, "POWER.md"), []byte("---\nname: test-power\n---\n\nbody\n"))

	_, err := Install(context.Background(), InstallOptions{
		Source:    PowerSource{Kind: SourceLocal, Path: src},
		TargetDir: target,
	})
	if err == nil || !strings.Contains(err.Error(), "description or displayName") {
		t.Fatalf("Install() error = %v, want description/displayName error", err)
	}
	assertDirEmpty(t, target)
}

func TestInstall_GitLabURLRejected(t *testing.T) {
	_, err := ParsePowerSource("https://gitlab.com/o/r/-/tree/main/sub")
	if err == nil || !strings.Contains(err.Error(), "GitLab") {
		t.Fatalf("ParsePowerSource() error = %v, want GitLab rejection", err)
	}
}

func TestInstall_SteeringIsFile(t *testing.T) {
	target := t.TempDir()
	src := t.TempDir()
	writeFileForTest(t, filepath.Join(src, "POWER.md"), []byte("---\nname: steering-file\ndescription: test\n---\n\nbody\n"))
	writeFileForTest(t, filepath.Join(src, "steering"), []byte("not a directory"))

	_, err := Install(context.Background(), InstallOptions{
		Source:    PowerSource{Kind: SourceLocal, Path: src},
		TargetDir: target,
	})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("Install() error = %v, want steering directory error", err)
	}
	assertDirEmpty(t, target)
}

func TestInstall_PowerNameWithBadChars(t *testing.T) {
	target := t.TempDir()
	src := t.TempDir()
	writeFileForTest(t, filepath.Join(src, "POWER.md"), []byte("---\nname: foo bar\ndescription: test\n---\n\nbody\n"))

	_, err := Install(context.Background(), InstallOptions{
		Source:    PowerSource{Kind: SourceLocal, Path: src},
		TargetDir: target,
	})
	if err == nil || !strings.Contains(err.Error(), "valid name") {
		t.Fatalf("Install() error = %v, want invalid name error", err)
	}
	assertDirEmpty(t, target)
}

func TestInstall_Timeout(t *testing.T) {
	restore := swapLocalFetcher(func() Fetcher {
		return fakeFetcher{
			fetch: func(ctx context.Context, _ PowerSource) (string, string, func(), error) {
				select {
				case <-ctx.Done():
					return "", "", func() {}, ctx.Err()
				case <-time.After(200 * time.Millisecond):
					return t.TempDir(), "", func() {}, nil
				}
			},
		}
	})
	defer restore()

	_, err := Install(context.Background(), InstallOptions{
		Source:    PowerSource{Kind: SourceLocal, Path: "/irrelevant"},
		TargetDir: t.TempDir(),
		Timeout:   time.Millisecond,
	})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Install() error = %v, want deadline exceeded", err)
	}
}

func TestInstall_CleanupRunsOnError(t *testing.T) {
	cleaned := false
	restore := swapLocalFetcher(func() Fetcher {
		return fakeFetcher{
			fetch: func(context.Context, PowerSource) (string, string, func(), error) {
				return "", "", func() { cleaned = true }, errors.New("boom")
			},
		}
	})
	defer restore()

	_, err := Install(context.Background(), InstallOptions{
		Source:    PowerSource{Kind: SourceLocal, Path: "/irrelevant"},
		TargetDir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Install() error = %v, want fetch error", err)
	}
	if !cleaned {
		t.Fatal("cleanup was not called on error")
	}
}

type fakeFetcher struct {
	fetch func(ctx context.Context, src PowerSource) (string, string, func(), error)
}

func (f fakeFetcher) Fetch(ctx context.Context, src PowerSource) (string, string, func(), error) {
	return f.fetch(ctx, src)
}

func swapLocalFetcher(factory func() Fetcher) func() {
	prev := newLocalFetcher
	newLocalFetcher = factory
	return func() {
		newLocalFetcher = prev
	}
}

func mustReadPower(t *testing.T, dir string) *PowerManifest {
	t.Helper()

	manifest, err := readPower(dir)
	if err != nil {
		t.Fatalf("readPower() error = %v", err)
	}
	return manifest
}

func readMCPJSON(t *testing.T, path string) convert.MCPConfig {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var config convert.MCPConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return config
}

func writeFileForTest(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func mustResourcePaths(t *testing.T, powerDir string) []string {
	t.Helper()

	got, err := listInstalledResourcePaths(powerDir)
	if err != nil {
		t.Fatalf("listInstalledResourcePaths() error = %v", err)
	}
	return got
}

func assertPowerPackageMatchesFixture(t *testing.T, gotDir, wantDir string) {
	t.Helper()

	got := snapshotDirExcluding(t, gotDir, map[string]struct{}{"SKILL.md": {}})
	want := snapshotDirExcluding(t, wantDir, map[string]struct{}{"SKILL.md": {}})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("power package mismatch\ngot=%v\nwant=%v", got, want)
	}
}

func assertResourcePaths(t *testing.T, got, want []string) {
	t.Helper()

	for i := range got {
		got[i] = filepath.ToSlash(got[i])
	}
	for i := range want {
		want[i] = filepath.ToSlash(want[i])
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resource paths mismatch\ngot=%v\nwant=%v", got, want)
	}
}

func expectedResourcePathsForFixture(target, name, fixture string) []string {
	paths := []string{PowerDocPath(target, name)}
	switch fixture {
	case "sample-power", "no-mcp":
		paths = append(paths,
			filepath.Join(PowerDir(target, name), "steering", "conventions.md"),
			filepath.Join(PowerDir(target, name), "steering", "style.md"),
		)
	}
	return paths
}

func assertNoGeneratedSkill(t *testing.T, powerDir string) {
	t.Helper()

	_, err := os.Stat(filepath.Join(powerDir, "SKILL.md"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SKILL.md should not exist, err=%v", err)
	}
}

func repoPowerFixtureRoot() string {
	return filepath.Join("..", "..", "testdata", "power")
}

func testInstallForeignPowerDirRefused(t *testing.T, force bool) {
	t.Helper()

	target := t.TempDir()
	powerDir := PowerDir(target, "sample-power")
	writeFileForTest(t, filepath.Join(powerDir, "notes.txt"), []byte("foreign body\n"))
	before, err := os.ReadFile(filepath.Join(powerDir, "notes.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	_, err = Install(context.Background(), InstallOptions{
		Source:    PowerSource{Kind: SourceLocal, Path: filepath.Join(repoPowerFixtureRoot(), "sample-power", "input")},
		TargetDir: target,
		Force:     force,
	})
	if err == nil || !strings.Contains(err.Error(), "not kapm power-managed") {
		t.Fatalf("Install() error = %v, want foreign power refusal", err)
	}
	after, err := os.ReadFile(filepath.Join(powerDir, "notes.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("foreign skill changed unexpectedly\nbefore=%s\nafter=%s", before, after)
	}
}

func snapshotDir(t *testing.T, root string) map[string]string {
	t.Helper()

	files := make(map[string]string)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk(%q): %v", root, err)
	}
	return files
}

func snapshotDirExcluding(t *testing.T, root string, excluded map[string]struct{}) map[string]string {
	t.Helper()

	files := make(map[string]string)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if _, ok := excluded[rel]; ok {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk(%q): %v", root, err)
	}
	return files
}

func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory %q is not empty: %v", dir, entries)
	}
}
