package convert_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kapmcli/kapm/internal/convert"
	"github.com/kapmcli/kapm/internal/testutil"
)

func TestConvertMCPBasic(t *testing.T) {
	t.Parallel()
	runConverterMCPTest(t, filepath.Join(repoTestdataRoot(), "convert", "mcp", "basic", "input"), filepath.Join(repoTestdataRoot(), "convert", "mcp", "basic", "expected"))
}

func TestConvertMCPToolsDropped(t *testing.T) {
	t.Parallel()
	runConverterMCPTest(t, filepath.Join(repoTestdataRoot(), "convert", "mcp", "tools-warning", "input"), filepath.Join(repoTestdataRoot(), "convert", "mcp", "tools-warning", "expected"))
}

func TestConvertMCPNoMCPDeps(t *testing.T) {
	t.Parallel()

	dst := t.TempDir()
	if err := convert.ConvertMCP(filepath.Join(repoTestdataRoot(), "convert", "mcp", "no-mcp-deps", "input"), dst, false); err != nil {
		t.Fatalf("ConvertMCP() error = %v", err)
	}

	testutil.AssertDirEqual(t, dst, t.TempDir())
}

func TestConvertMCPMissingCommandAndURL(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFileForTest(t, filepath.Join(root, "apm.yml"), []byte("name: test-pkg\nversion: 1.0.0\ndependencies:\n  mcp:\n    - name: broken-server\n"))
	if err := os.MkdirAll(filepath.Join(root, ".apm"), 0o755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}

	err := convert.ConvertMCP(filepath.Join(root, ".apm"), t.TempDir(), false)
	if err == nil {
		t.Fatal("ConvertMCP() error = nil, want validation error")
	}
}

func TestConvertMCPRejectsLargeManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	data := make([]byte, 2<<20)
	for i := range data {
		data[i] = 'x'
	}
	writeFileForTest(t, filepath.Join(root, "apm.yml"), data)
	if err := os.MkdirAll(filepath.Join(root, ".apm"), 0o755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}

	err := convert.ConvertMCP(filepath.Join(root, ".apm"), t.TempDir(), false)
	if err == nil {
		t.Fatal("ConvertMCP() error = nil, want error for oversized manifest")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("want 'too large' in error, got %q", err.Error())
	}
}

func TestConvertMCPMergeSkipsExisting(t *testing.T) {
	t.Parallel()

	src := filepath.Join(repoTestdataRoot(), "convert", "mcp", "basic", "input")
	dst := t.TempDir()
	writeMCPJSON(t, filepath.Join(dst, "settings", "mcp.json"), map[string]mcpServerEntryForTest{
		"stdio-server": {
			Command: "existing-python",
			Args:    []string{"-m", "existing_server"},
		},
		"existing-server": {
			URL: "https://existing.example/mcp",
		},
	})

	if err := convert.ConvertMCP(src, dst, false); err != nil {
		t.Fatalf("ConvertMCP() error = %v", err)
	}

	assertMCPServers(t, filepath.Join(dst, "settings", "mcp.json"), map[string]mcpServerEntryForTest{
		"stdio-server": {
			Command: "existing-python",
			Args:    []string{"-m", "existing_server"},
		},
		"existing-server": {
			URL: "https://existing.example/mcp",
		},
		"http-server": {
			URL: "https://example.com/mcp",
			Headers: map[string]string{
				"Authorization": "Bearer ${TOKEN}",
			},
		},
	})
}

func TestConvertMCPMergeForce(t *testing.T) {
	t.Parallel()

	src := filepath.Join(repoTestdataRoot(), "convert", "mcp", "basic", "input")
	dst := t.TempDir()
	writeMCPJSON(t, filepath.Join(dst, "settings", "mcp.json"), map[string]mcpServerEntryForTest{
		"stdio-server": {
			Command: "old-python",
			Args:    []string{"old.py"},
		},
		"existing-server": {
			URL: "https://existing.example/mcp",
		},
	})

	if err := convert.ConvertMCP(src, dst, true); err != nil {
		t.Fatalf("ConvertMCP() error = %v", err)
	}

	assertMCPServers(t, filepath.Join(dst, "settings", "mcp.json"), map[string]mcpServerEntryForTest{
		"stdio-server": {
			Command: "python",
			Args:    []string{"-m", "my_server"},
			Env: map[string]string{
				"API_KEY": "${MY_KEY}",
			},
		},
		"http-server": {
			URL: "https://example.com/mcp",
			Headers: map[string]string{
				"Authorization": "Bearer ${TOKEN}",
			},
		},
		"existing-server": {
			URL: "https://existing.example/mcp",
		},
	})
}

func runConverterMCPTest(t *testing.T, srcFixture, expectedFixture string) {
	t.Helper()

	dst := t.TempDir()
	if err := convert.ConvertMCP(srcFixture, dst, false); err != nil {
		t.Fatalf("ConvertMCP() error = %v", err)
	}
	testutil.AssertDirEqual(t, dst, expectedFixture)
}

type mcpConfigForTest struct {
	MCPServers map[string]mcpServerEntryForTest `json:"mcpServers"`
}

type mcpServerEntryForTest struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

func writeMCPJSON(t *testing.T, path string, servers map[string]mcpServerEntryForTest) {
	t.Helper()

	data, err := json.MarshalIndent(mcpConfigForTest{MCPServers: servers}, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(): %v", err)
	}
	writeFileForTest(t, path, append(data, '\n'))
}

func assertMCPServers(t *testing.T, path string, want map[string]mcpServerEntryForTest) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}

	var got mcpConfigForTest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal(%q): %v", path, err)
	}

	if !jsonEqual(t, got.MCPServers, want) {
		t.Fatalf("mcpServers mismatch\n got: %#v\nwant: %#v", got.MCPServers, want)
	}
}

func jsonEqual(t *testing.T, got, want any) bool {
	t.Helper()

	gotData, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal(got): %v", err)
	}
	wantData, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal(want): %v", err)
	}
	return string(gotData) == string(wantData)
}
