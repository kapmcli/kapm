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

type existingAgent struct {
	Path  string
	Raw   map[string]json.RawMessage
	Known agentKnownFields
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
		return fmt.Errorf("validate agent name: %w", err)
	}

	existing, err := loadExistingAgent(root, name)
	if err != nil {
		return err
	}

	p := cli.NewPrompter(opts.In, opts.Out)
	details, err := promptUpdateDetails(p, opts.Out, existing.Known)
	if err != nil {
		return fmt.Errorf("prompt update details: %w", err)
	}

	if updateDetailsMatchKnown(details, existing.Known) {
		if _, err := fmt.Fprintln(opts.Out, "No changes."); err != nil {
			return fmt.Errorf("write no changes: %w", err)
		}
		return nil
	}

	if err := applyUpdateDetails(existing.Raw, existing.Known, details); err != nil {
		return err
	}
	return writeAgentRawJSON(existing.Path, existing.Raw)
}

func loadExistingAgent(root, name string) (existingAgent, error) {
	agentPath := AgentFile(root, name)
	if err := validatePath(root, filepath.Join(root, paths.KiroDir)); err != nil {
		return existingAgent{}, fmt.Errorf("validate .kiro dir: %w", err)
	}
	if err := validatePath(root, filepath.Join(root, paths.KiroDir, paths.AgentsSubdir)); err != nil {
		return existingAgent{}, fmt.Errorf("validate agents subdir: %w", err)
	}
	if err := validatePath(root, agentPath); err != nil {
		return existingAgent{}, fmt.Errorf("validate agent path: %w", err)
	}

	rawMap, existingData, err := readAgentRawJSON(agentPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return existingAgent{}, fmt.Errorf("agent %q does not exist; use `kapm agent generate` to create it", name)
		}
		return existingAgent{}, fmt.Errorf("read %q: %w", agentPath, err)
	}

	var known agentKnownFields
	if err := json.Unmarshal(existingData, &known); err != nil {
		return existingAgent{}, fmt.Errorf("unmarshal known fields %q: %w", agentPath, err)
	}
	return existingAgent{Path: agentPath, Raw: rawMap, Known: known}, nil
}

func updateDetailsMatchKnown(details updateDetails, known agentKnownFields) bool {
	return details.Model == known.Model &&
		slices.Equal(details.Tools, known.Tools) &&
		slices.Equal(details.AllowedTools, known.AllowedTools) &&
		slices.Equal(details.Resources, known.Resources)
}

func applyUpdateDetails(rawMap map[string]json.RawMessage, known agentKnownFields, details updateDetails) error {
	if details.Model != known.Model {
		if err := setRawJSONField(rawMap, "model", details.Model); err != nil {
			return fmt.Errorf("set model field: %w", err)
		}
	}
	if !slices.Equal(details.Tools, known.Tools) {
		if err := setRawJSONField(rawMap, "tools", ensureNonNil(details.Tools)); err != nil {
			return fmt.Errorf("set tools field: %w", err)
		}
	}
	if !slices.Equal(details.AllowedTools, known.AllowedTools) {
		if err := setRawJSONField(rawMap, "allowedTools", ensureNonNil(details.AllowedTools)); err != nil {
			return fmt.Errorf("set allowedTools field: %w", err)
		}
	}
	if !slices.Equal(details.Resources, known.Resources) {
		if len(details.Resources) == 0 {
			delete(rawMap, "resources")
		} else if err := setRawJSONField(rawMap, "resources", details.Resources); err != nil {
			return fmt.Errorf("set resources field: %w", err)
		}
	}
	return nil
}

func promptUpdateDetails(p *cli.Prompter, out io.Writer, known agentKnownFields) (updateDetails, error) {
	model, err := p.Ask("model", known.Model)
	if err != nil {
		return updateDetails{}, err
	}
	if strings.TrimSpace(model) == "" {
		return updateDetails{}, errors.New("model cannot be empty")
	}

	fields, err := promptAgentCoreFields(p, out, agentFieldDefaults{
		Model:                 model,
		Tools:                 known.Tools,
		AllowedTools:          known.AllowedTools,
		Resources:             known.Resources,
		ShowExistingResources: true,
	})
	if err != nil {
		return updateDetails{}, err
	}
	return updateDetails(fields), nil
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
