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

type updateDetails struct {
	Model        string
	Tools        []string
	AllowedTools []string
	Resources    []string
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
	details, err := promptUpdateDetails(p, opts.Out, known)
	if err != nil {
		return err
	}

	if details.Model == known.Model && slices.Equal(details.Tools, known.Tools) && slices.Equal(details.AllowedTools, known.AllowedTools) && slices.Equal(details.Resources, known.Resources) {
		if _, err := fmt.Fprintln(opts.Out, "No changes."); err != nil {
			return err
		}
		return nil
	}

	if details.Model != known.Model {
		if err := setRawJSONField(rawMap, "model", details.Model); err != nil {
			return err
		}
	}
	if !slices.Equal(details.Tools, known.Tools) {
		if err := setRawJSONField(rawMap, "tools", ensureNonNil(details.Tools)); err != nil {
			return err
		}
	}
	if !slices.Equal(details.AllowedTools, known.AllowedTools) {
		if err := setRawJSONField(rawMap, "allowedTools", ensureNonNil(details.AllowedTools)); err != nil {
			return err
		}
	}
	if !slices.Equal(details.Resources, known.Resources) {
		if len(details.Resources) == 0 {
			delete(rawMap, "resources")
		} else if err := setRawJSONField(rawMap, "resources", details.Resources); err != nil {
			return err
		}
	}

	return writeAgentRawJSON(agentPath, rawMap)
}

func promptUpdateDetails(p *cli.Prompter, out io.Writer, known agentKnownFields) (updateDetails, error) {
	model, err := p.Ask("model", known.Model)
	if err != nil {
		return updateDetails{}, err
	}
	if strings.TrimSpace(model) == "" {
		return updateDetails{}, fmt.Errorf("model cannot be empty")
	}

	// Update offers the full set of available tools. Existing tool values are re-surfaced
	// in the prompt with their current selection state.
	tools, err := p.MultiSelectWithDefaults("tools", apmconfig.AvailableAgentTools, defaultToolIndices(apmconfig.AvailableAgentTools, known.Tools))
	if err != nil {
		return updateDetails{}, err
	}

	allowedTools, err := p.MultiSelectWithDefaults("allowedTools", apmconfig.AvailableAgentTools, defaultToolIndices(apmconfig.AvailableAgentTools, known.AllowedTools))
	if err != nil {
		return updateDetails{}, err
	}

	if _, err := fmt.Fprintf(out, "Current resources: %v\n", known.Resources); err != nil {
		return updateDetails{}, err
	}
	if _, err := fmt.Fprintln(out, "Enter new resources (blank line to keep current):"); err != nil {
		return updateDetails{}, err
	}
	resources, err := p.MultiInput("resources")
	if err != nil {
		return updateDetails{}, err
	}
	if len(resources) == 0 {
		resources = slices.Clone(known.Resources)
	} else {
		resources = append([]string(nil), resources...)
	}

	return updateDetails{
		Model:        model,
		Tools:        tools,
		AllowedTools: allowedTools,
		Resources:    resources,
	}, nil
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
