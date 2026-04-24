package agent

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"github.com/kapmcli/kapm/internal/cli"
	"github.com/kapmcli/kapm/internal/fileutil"
	"github.com/kapmcli/kapm/internal/paths"
)

var knownModels = []string{
	"claude-sonnet-4-5",
	"claude-sonnet-4-5-20251001",
	"claude-opus-4-5",
	"gpt-4o",
	"gpt-4.1",
	"o3",
	"enter custom...",
}

type agentConfig = apmconfig.AgentConfig

type GenerateOptions struct {
	Root  string
	Force bool
	In    io.Reader
	Out   io.Writer
}

// Generate interactively creates a new .kiro agent config and prompt file.
func Generate(opts GenerateOptions) error {
	return generate(opts)
}

func generate(opts GenerateOptions) error {
	root := opts.Root
	applyDefaults(&root, &opts.In, &opts.Out)

	p := cli.NewPrompter(opts.In, opts.Out)

	name, err := p.Ask("name", "")
	if err != nil {
		return err
	}
	name, err = validateAndNormalizeName(name)
	if err != nil {
		return err
	}

	agentPath := AgentFile(root, name)
	promptPath := AgentPromptFile(root, name)
	if err := ensureAgentPaths(root, name, agentPath, promptPath, opts.Force); err != nil {
		return err
	}

	description, err := p.Ask("description", "")
	if err != nil {
		return err
	}
	if strings.TrimSpace(description) == "" {
		return fmt.Errorf("description cannot be empty")
	}

	model, err := p.Select("model", knownModels, 0)
	if err != nil {
		return err
	}
	if model == knownModels[len(knownModels)-1] {
		model, err = p.Ask("custom model", "")
		if err != nil {
			return err
		}
		model = strings.TrimSpace(model)
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("custom model cannot be empty")
		}
	}

	presets := []string{"default", "orchestrator"}
	preset, err := p.Select("preset", presets, 0)
	if err != nil {
		return err
	}

	var presetTools []string
	if preset == presets[1] {
		presetTools = apmconfig.OrchestratorAgentTools
	} else {
		presetTools = apmconfig.DefaultAgentTools
	}
	tools, err := p.MultiSelectWithDefaults("tools", apmconfig.AvailableAgentTools, defaultToolIndices(apmconfig.AvailableAgentTools, presetTools))
	if err != nil {
		return err
	}

	allowedTools, err := p.MultiSelectWithDefaults("allowedTools (defaults to selected tools, press Enter to accept)", apmconfig.AvailableAgentTools, defaultToolIndices(apmconfig.AvailableAgentTools, tools))
	if err != nil {
		return err
	}

	resources := []string{apmconfig.DefaultSkillsResource}
	extraResources, err := p.MultiInput("additional resources")
	if err != nil {
		return err
	}
	resources = append(resources, extraResources...)

	config := agentConfig{
		Name:         name,
		Description:  description,
		Prompt:       fmt.Sprintf("file://../agent-prompts/%s.md", name),
		Model:        model,
		Tools:        append([]string(nil), tools...),
		AllowedTools: append([]string(nil), allowedTools...),
		Resources:    append([]string(nil), resources...),
	}

	jsonData, err := apmconfig.MarshalIndentedJSON(config)
	if err != nil {
		return fmt.Errorf("marshal agent json: %w", err)
	}
	if _, err := writeValidatedPair(root, agentPath, jsonData, promptPath, []byte("# "+name+"\n"), opts.Force); err != nil {
		return err
	}

	return nil
}

func ensureAgentPaths(root, name, agentPath, promptPath string, force bool) error {
	for _, dir := range []string{
		filepath.Join(root, paths.KiroDir),
		filepath.Join(root, paths.KiroDir, paths.AgentsSubdir),
		filepath.Join(root, paths.KiroDir, paths.AgentPromptsDir),
	} {
		if err := validatePath(root, dir); err != nil {
			return err
		}
	}

	for _, path := range []string{agentPath, promptPath} {
		isLink, err := fileutil.IsSymlinkPath(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("lstat %q: %w", path, err)
		}
		if isLink {
			return fmt.Errorf("path %q must not be a symlink", path)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("lstat %q: %w", path, err)
		}
		if info.IsDir() {
			return fmt.Errorf("path %q must not be a directory", path)
		}
		if !force {
			return fmt.Errorf("agent %q already exists; use --force to overwrite", name)
		}
	}

	return nil
}
