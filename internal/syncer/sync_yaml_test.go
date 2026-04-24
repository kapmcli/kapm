package syncer

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestApmDependency_ReferenceNotSerialized(t *testing.T) {
	t.Parallel()

	d := apmDependency{Reference: "secret"}
	data, err := yaml.Marshal(d)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}
	if strings.Contains(string(data), "secret") {
		t.Fatalf("yaml.Marshal() output contains %q, want it omitted:\n%s", "secret", data)
	}
}

func TestRun_ManifestReadOnce(t *testing.T) {
	// Not parallel: mutates package-level readFileFunc.
	root := t.TempDir()

	// Write a valid apm.yml with two APM module dependencies.
	apmYML := []byte("name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - github.com/org/pkg1\n    - github.com/org/pkg2\n")
	if err := os.WriteFile(filepath.Join(root, "apm.yml"), apmYML, 0o644); err != nil {
		t.Fatal(err)
	}

	// Set up two module packages so runConverters is called twice.
	for _, pkg := range []string{"pkg1", "pkg2"} {
		apmDir := filepath.Join(root, "apm_modules", "org", pkg, ".apm")
		if err := os.MkdirAll(apmDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var readCount atomic.Int32
	orig := readFileFunc
	readFileFunc = func(name string) ([]byte, error) {
		if filepath.Base(name) == "apm.yml" {
			readCount.Add(1)
		}
		return orig(name)
	}
	t.Cleanup(func() { readFileFunc = orig })

	if err := Run(Options{Root: root}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if n := readCount.Load(); n != 1 {
		t.Errorf("apm.yml read %d times, want 1", n)
	}
}
