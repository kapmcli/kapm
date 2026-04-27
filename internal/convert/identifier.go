package convert

import (
	"fmt"
	"strings"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"gopkg.in/yaml.v3"
)

// Metadata holds the typed frontmatter fields read by the convert package.
type Metadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Model       string `yaml:"model"`
}

// metadataFrom decodes a frontmatter map into a typed Metadata struct.
// Unknown fields are silently ignored.
func metadataFrom(meta map[string]any, path string) (Metadata, error) {
	raw, err := yaml.Marshal(meta)
	if err != nil {
		return Metadata{}, fmt.Errorf("convert parse %q: %w", path, err)
	}
	var m Metadata
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return Metadata{}, fmt.Errorf("convert parse %q: %w", path, err)
	}
	if strings.TrimSpace(m.Description) == "" {
		return Metadata{}, fmt.Errorf("convert parse %q: missing %q", path, "description")
	}
	return m, nil
}

func sanitizeIdentifier(value, field, path string) (string, error) {
	trimmed, err := apmconfig.ValidateIdentifier(value)
	if err != nil {
		return "", fmt.Errorf("convert parse %q: invalid %q", path, field)
	}
	return trimmed, nil
}
