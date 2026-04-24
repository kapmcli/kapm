package syncer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kapmcli/kapm/internal/syncer"
	"github.com/kapmcli/kapm/internal/testutil"
)

func TestSyncRejectsLargeManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// Write a 2 MiB apm.yml (well over the 1 MiB limit)
	data := make([]byte, 2<<20)
	for i := range data {
		data[i] = 'x'
	}
	writeTestFile(t, filepath.Join(root, "apm.yml"), data)

	err := syncer.Run(syncer.Options{Root: root})
	if err == nil {
		t.Fatal("Run() error = nil, want error for oversized manifest")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("want 'too large' in error, got %q", err.Error())
	}
}

func TestSyncApmModules(t *testing.T) {
	t.Parallel()
	runSyncGoldenTest(t, "modules-only")
}

func TestSyncLocalApm(t *testing.T) {
	t.Parallel()
	runSyncGoldenTest(t, "local-only")
}

func TestSyncMCPLocal(t *testing.T) {
	t.Parallel()
	runSyncGoldenTest(t, "mcp-local")
}

func TestSyncForce(t *testing.T) {
	t.Parallel()
	runSyncGoldenTestWithForce(t, "force-local-overrides")
}

func TestSyncLocalOverridesModulesWithoutForce(t *testing.T) {
	t.Parallel()
	runSyncGoldenTest(t, "local-overrides-modules")
}

func TestSyncMultiPackageOrder(t *testing.T) {
	t.Parallel()
	runSyncGoldenTest(t, "multi-package-order")
}

func TestSyncVirtualSkill(t *testing.T) {
	t.Parallel()
	runSyncGoldenTest(t, "virtual-skill")
}

func TestSyncVirtualPrompt(t *testing.T) {
	t.Parallel()
	runSyncGoldenTest(t, "virtual-prompt")
}

func TestRun_AcceptsFullSchemaManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "apm.yml"), []byte(
		"name: proj\nversion: 1.0.0\ndescription: d\nauthor: a\n"+
			"dependencies:\n  apm: []\n  mcp: []\nscripts: {}\n"))

	if err := syncer.Run(syncer.Options{Root: root}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestSyncEmpty(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := syncer.Run(syncer.Options{Root: root}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRun_RejectsUnknownManifestField(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "apm.yml"), []byte("dependecies:\n  apm: []\n"))

	err := syncer.Run(syncer.Options{Root: root})
	if err == nil {
		t.Fatal("Run() error = nil, want error for unknown field")
	}
	if !strings.Contains(err.Error(), "dependecies") {
		t.Fatalf("Run() error = %q, want error mentioning %q", err.Error(), "dependecies")
	}
}

func TestRun_WrapsWalkError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// Create apm_modules with a sub-dir that has no-read perms to trigger WalkDir error.
	modDir := filepath.Join(root, "apm_modules", "org", "pkg")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "apm_modules", "org"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "apm_modules", "org"), 0o755) })

	err := syncer.Run(syncer.Options{Root: root})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sync read") {
		t.Errorf("error %q does not contain %q", err.Error(), "sync read")
	}
}

func runSyncGoldenTest(t *testing.T, fixture string) {
	t.Helper()
	runSyncGoldenTestWithOptions(t, fixture, syncer.Options{})
}

func runSyncGoldenTestWithForce(t *testing.T, fixture string) {
	t.Helper()
	runSyncGoldenTestWithOptions(t, fixture, syncer.Options{Force: true})
}

func runSyncGoldenTestWithOptions(t *testing.T, fixture string, opts syncer.Options) {
	t.Helper()

	root := t.TempDir()
	input := filepath.Join(repoTestdataRoot(), "sync", fixture, "input")
	testutil.CopyDir(t, input, root)
	opts.Root = root

	if err := syncer.Run(opts); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	testutil.AssertDirEqual(t, filepath.Join(root, ".kiro"), filepath.Join(repoTestdataRoot(), "sync", fixture, "expected", ".kiro"))
}

func repoTestdataRoot() string {
	return filepath.Join("..", "..", "testdata")
}


func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
