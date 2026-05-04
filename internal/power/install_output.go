package power

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// WriteInstallResult writes the CLI summary and follow-up suggestions for an installed Power.
func WriteInstallResult(w io.Writer, result *Result) {
	_, _ = fmt.Fprintf(w, "Installed power %q to %s\n", result.Name, result.PowerDir)
	if result.ResolvedCommit != "" {
		_, _ = fmt.Fprintf(w, "Resolved commit: %s\n", result.ResolvedCommit)
	}
	writeInstallSuggestions(w, result)
	for _, warning := range result.Warnings {
		_, _ = fmt.Fprintf(w, "Warning: %s\n", warning)
	}
}

func writeInstallSuggestions(w io.Writer, result *Result) {
	_, _ = fmt.Fprintln(w, "Suggested custom agent config:")
	_, _ = fmt.Fprintln(w, `"resources": [`)
	for i, resourcePath := range result.ResourcePaths {
		suffix := ","
		if i == len(result.ResourcePaths)-1 {
			suffix = ""
		}
		_, _ = fmt.Fprintf(w, "  \"file://%s\"%s\n", filepath.ToSlash(resourcePath), suffix)
	}
	_, _ = fmt.Fprintln(w, `]`)

	if result.MCPConfigPath != "" {
		mcpServers, err := readMCPServers(result.MCPConfigPath)
		if err != nil {
			_, _ = fmt.Fprintf(w, "MCP config: copy %s into the agent's mcpServers field (could not render snippet: %v)\n", result.MCPConfigPath, err)
		} else {
			_, _ = fmt.Fprintln(w, `"mcpServers":`)
			_, _ = w.Write(mcpServers)
			if len(mcpServers) == 0 || mcpServers[len(mcpServers)-1] != '\n' {
				_, _ = fmt.Fprintln(w)
			}
		}
	}

	if result.HooksDir != "" {
		hookFiles, err := listPowerHookFiles(result.HooksDir)
		if err != nil {
			_, _ = fmt.Fprintf(w, "Hooks: adapt files under %s into the agent's hooks field (could not list files: %v)\n", result.HooksDir, err)
		} else {
			_, _ = fmt.Fprintln(w, `Hook files to adapt into the agent's "hooks" field:`)
			for _, hookFile := range hookFiles {
				_, _ = fmt.Fprintf(w, "- %s\n", hookFile)
			}
		}
	}

	_, _ = fmt.Fprintf(w, "Remove: rm -rf %s\n", result.PowerDir)
}

func readMCPServers(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var config struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return json.MarshalIndent(config.MCPServers, "", "  ")
}

func listPowerHookFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		files = append(files, filepath.ToSlash(path))
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(files)
	return files, nil
}
