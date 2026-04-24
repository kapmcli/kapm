package convert

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"github.com/kapmcli/kapm/internal/fileutil"
	"github.com/kapmcli/kapm/internal/paths"
	"gopkg.in/yaml.v3"
)

type mcpManifest struct {
	Name         string `yaml:"name"`
	Version      string `yaml:"version"`
	Dependencies struct {
		APM []yaml.Node     `yaml:"apm"`
		MCP []MCPDependency `yaml:"mcp"`
	} `yaml:"dependencies"`
}

// MCPDependency describes a single MCP server entry in apm.yml.
type MCPDependency struct {
	Name      string            `yaml:"name"`
	Command   string            `yaml:"command"`
	Args      []string          `yaml:"args"`
	Env       map[string]string `yaml:"env"`
	URL       string            `yaml:"url"`
	Headers   map[string]string `yaml:"headers"`
	Tools     []string          `yaml:"tools"`
	Transport string            `yaml:"transport"`
	Registry  string            `yaml:"registry"`
	Package   string            `yaml:"package"`
	Version   string            `yaml:"version"`
}

type mcpConfig struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

type mcpServerEntry struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// ConvertMCP merges MCP dependencies from `apm.yml` into `.kiro/settings/mcp.json`.
func ConvertMCP(srcDir, dstDir string, force bool) error {
	_, err := ConvertMCPWithReport(srcDir, dstDir, force)
	return err
}

// ConvertMCPWithReport converts MCP dependencies and reports converted or skipped servers.
func ConvertMCPWithReport(srcDir, dstDir string, force bool) (Report, error) {
	manifestPath := filepath.Join(filepath.Dir(srcDir), paths.APMManifest)
	info, statErr := os.Stat(manifestPath)
	if statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return Report{}, nil
		}
		return Report{}, wrapConvertError("mcp", manifestPath, statErr)
	}
	if info.Size() > apmconfig.MaxManifestBytes {
		return Report{}, fmt.Errorf("manifest %q too large (%d bytes, limit %d)", manifestPath, info.Size(), apmconfig.MaxManifestBytes)
	}
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return Report{}, wrapConvertError("mcp", manifestPath, err)
	}

	var manifest mcpManifest
	dec := yaml.NewDecoder(bytes.NewReader(manifestData))
	dec.KnownFields(true)
	if err := dec.Decode(&manifest); err != nil {
		return Report{}, fmt.Errorf("parse apm.yml: %w", err)
	}
	return convertMCPDeps(manifest.Dependencies.MCP, dstDir, force, manifestPath)
}

// ConvertMCPWithDeps converts pre-parsed MCP dependencies into `.kiro/settings/mcp.json`.
func ConvertMCPWithDeps(deps []MCPDependency, srcDir, dstDir string, force bool) (Report, error) {
	manifestPath := filepath.Join(filepath.Dir(srcDir), paths.APMManifest)
	return convertMCPDeps(deps, dstDir, force, manifestPath)
}

func convertMCPDeps(deps []MCPDependency, dstDir string, force bool, manifestPath string) (Report, error) {
	if len(deps) == 0 {
		return Report{}, nil
	}

	dstPath := filepath.Join(dstDir, paths.SettingsSubdir, paths.MCPFile)
	config := mcpConfig{MCPServers: map[string]mcpServerEntry{}}
	if data, err := os.ReadFile(dstPath); err == nil {
		if err := json.Unmarshal(data, &config); err != nil {
			return Report{}, fmt.Errorf("convert mcp parse existing %q: %w", dstPath, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Report{}, wrapConvertError("mcp", dstPath, err)
	}
	if config.MCPServers == nil {
		config.MCPServers = map[string]mcpServerEntry{}
	}

	report := Report{}
	for _, dep := range deps {
		name := strings.TrimSpace(dep.Name)
		if name == "" {
			return Report{}, fmt.Errorf("convert mcp %q: missing name", manifestPath)
		}

		command := strings.TrimSpace(dep.Command)
		url := strings.TrimSpace(dep.URL)
		if command == "" && url == "" {
			return Report{}, fmt.Errorf("convert mcp %q: server %q requires command or url", manifestPath, name)
		}

		if _, exists := config.MCPServers[name]; exists && !force {
			slog.Warn("kapm skip existing mcp server", "server", name)
			report.Skipped++
			continue
		}

		if len(dep.Tools) > 0 {
			slog.Warn("kapm mcp tools field dropped", "server", name)
		}

		config.MCPServers[name] = mcpServerEntry{
			Command: command,
			Args:    append([]string(nil), dep.Args...),
			Env:     cloneStringMap(dep.Env),
			URL:     url,
			Headers: cloneStringMap(dep.Headers),
		}
		report.Converted++
	}

	if report.Converted == 0 {
		return report, nil
	}

	data, err := apmconfig.MarshalIndentedJSON(config)
	if err != nil {
		return Report{}, wrapConvertError("mcp", manifestPath, err)
	}
	if _, err := fileutil.WriteFileAtomic(dstPath, data, true); err != nil {
		return Report{}, wrapConvertError("mcp", dstPath, err)
	}

	return report, nil
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}

	dst := make(map[string]string, len(src))
	maps.Copy(dst, src)
	return dst
}
