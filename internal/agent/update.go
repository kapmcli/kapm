package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"github.com/kapmcli/kapm/internal/cli"
	"github.com/kapmcli/kapm/internal/paths"
)

// UpdateOptions configures an interactive agent update run.
type UpdateOptions struct {
	Root string
	Name string
	In   io.Reader
	Out  io.Writer
}

type agentKnownFields struct {
	Model        string   `json:"model"`
	Tools        []string `json:"tools"`
	AllowedTools []string `json:"allowedTools"`
	Resources    []string `json:"resources"`
}

// Update interactively updates an existing .kiro agent config.
func Update(opts UpdateOptions) error {
	if err := update(opts); err != nil {
		return fmt.Errorf("agent update: %w", err)
	}
	return nil
}

func update(opts UpdateOptions) error {
	root := opts.Root
	applyDefaults(&root, &opts.In, &opts.Out)

	name, err := validateAndNormalizeName(opts.Name)
	if err != nil {
		return err
	}

	agentPath := AgentFile(root, name)
	if err := validatePath(root, filepath.Join(root, paths.KiroDir)); err != nil {
		return err
	}
	if err := validatePath(root, filepath.Join(root, paths.KiroDir, paths.AgentsSubdir)); err != nil {
		return err
	}
	if err := validatePath(root, agentPath); err != nil {
		return err
	}
	rawMap, existingData, err := readAgentRawJSON(agentPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("agent %q does not exist; use `kapm agent generate` to create it", name)
		}
		return fmt.Errorf("read %q: %w", agentPath, err)
	}

	var known agentKnownFields
	if err := json.Unmarshal(existingData, &known); err != nil {
		return fmt.Errorf("unmarshal known fields %q: %w", agentPath, err)
	}

	p := cli.NewPrompter(opts.In, opts.Out)

	model, err := p.Ask("model", known.Model)
	if err != nil {
		return err
	}
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("model cannot be empty")
	}

	// Update offers the full set of available tools. Existing tool values are re-surfaced
	// in the prompt with their current selection state.
	tools, err := p.MultiSelectWithDefaults("tools", apmconfig.AvailableAgentTools, defaultToolIndices(apmconfig.AvailableAgentTools, known.Tools))
	if err != nil {
		return err
	}

	allowedTools, err := p.MultiSelectWithDefaults("allowedTools", apmconfig.AvailableAgentTools, defaultToolIndices(apmconfig.AvailableAgentTools, known.AllowedTools))
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(opts.Out, "Current resources: %v\n", known.Resources); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(opts.Out, "Enter new resources (blank line to keep current):"); err != nil {
		return err
	}
	resources, err := p.MultiInput("resources")
	if err != nil {
		return err
	}
	if len(resources) == 0 {
		resources = slices.Clone(known.Resources)
	} else {
		resources = append([]string(nil), resources...)
	}

	if model == known.Model && slices.Equal(tools, known.Tools) && slices.Equal(allowedTools, known.AllowedTools) && slices.Equal(resources, known.Resources) {
		if _, err := fmt.Fprintln(opts.Out, "No changes."); err != nil {
			return err
		}
		return nil
	}

	if model != known.Model {
		if err := setRawJSONField(rawMap, "model", model); err != nil {
			return err
		}
	}
	if !slices.Equal(tools, known.Tools) {
		if err := setRawJSONField(rawMap, "tools", ensureNonNil(tools)); err != nil {
			return err
		}
	}
	if !slices.Equal(allowedTools, known.AllowedTools) {
		if err := setRawJSONField(rawMap, "allowedTools", ensureNonNil(allowedTools)); err != nil {
			return err
		}
	}
	if !slices.Equal(resources, known.Resources) {
		if len(resources) == 0 {
			delete(rawMap, "resources")
		} else if err := setRawJSONField(rawMap, "resources", resources); err != nil {
			return err
		}
	}

	return writeAgentRawJSON(agentPath, rawMap)
}

// ensureNonNil returns a copy of values, converting nil to an empty slice
// so JSON marshaling produces [] instead of null.
func ensureNonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return slices.Clone(values)
}

func setRawJSONField(rawMap map[string]json.RawMessage, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal %q: %w", key, err)
	}
	rawMap[key] = json.RawMessage(data)
	return nil
}
