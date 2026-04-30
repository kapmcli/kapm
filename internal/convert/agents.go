package convert

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"github.com/kapmcli/kapm/internal/frontmatter"
	"github.com/kapmcli/kapm/internal/paths"
)

type agentConfig = apmconfig.AgentConfig

// ConvertAgents converts agent and legacy chatmode inputs into Kiro agent files.
func ConvertAgents(srcDir, dstDir string, force bool) error {
	_, err := ConvertAgentsWithReport(srcDir, dstDir, force)
	if err != nil {
		return fmt.Errorf("convert agents: %w", err)
	}
	return nil
}

// ConvertAgentsWithReport converts agent inputs and reports converted or skipped files.
func ConvertAgentsWithReport(srcDir, dstDir string, force bool) (Report, error) {
	report := Report{}
	for _, pattern := range []struct {
		subdir string
		glob   string
		suffix string
	}{
		{subdir: "agents", glob: "*.agent.md", suffix: ".agent.md"},
		{subdir: "chatmodes", glob: "*.chatmode.md", suffix: ".chatmode.md"},
	} {
		sub, err := convertDocumentsWithReport(srcDir, pattern.subdir, pattern.glob, "agents", force, func(path string, doc frontmatter.Document) (documentWriteTarget, error) {
			meta, err := metadataFrom(doc.Meta, path)
			if err != nil {
				return documentWriteTarget{}, wrapConvertError("agents", path, err)
			}

			name := strings.TrimSuffix(filepath.Base(path), pattern.suffix)
			name, err = sanitizeIdentifier(name, "name", path)
			if err != nil {
				return documentWriteTarget{}, wrapConvertError("agents", path, err)
			}
			if strings.TrimSpace(meta.Name) != "" {
				name, err = sanitizeIdentifier(meta.Name, "name", path)
				if err != nil {
					return documentWriteTarget{}, wrapConvertError("agents", path, err)
				}
			}

			config := agentConfig{
				Name:         name,
				Description:  meta.Description,
				Prompt:       fmt.Sprintf("file://../agent-prompts/%s.md", name),
				Tools:        append([]string(nil), apmconfig.DefaultAgentTools...),
				AllowedTools: append([]string(nil), apmconfig.DefaultAgentTools...),
				Resources:    []string{apmconfig.DefaultSkillsResource},
			}
			if meta.Model != "" {
				config.Model = meta.Model
			}

			jsonData, err := apmconfig.MarshalIndentedJSON(config)
			if err != nil {
				return documentWriteTarget{}, wrapConvertError("agents", path, err)
			}

			return documentWriteTarget{
				path:       filepath.Join(dstDir, paths.AgentsSubdir, name+".json"),
				data:       jsonData,
				secondPath: filepath.Join(dstDir, paths.AgentPromptsDir, name+".md"),
				secondData: []byte(bodyWithoutLeadingBlankLine(doc.Body)),
			}, nil
		})
		if err != nil {
			return Report{}, err
		}
		report.Add(sub)
	}

	return report, nil
}
