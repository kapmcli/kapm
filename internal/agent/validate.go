package agent

import (
	"fmt"
	"strings"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

func validateAndNormalizeName(name string) (string, error) {
	trimmed, err := apmconfig.ValidateIdentifier(name)
	if err != nil {
		if strings.TrimSpace(name) == "" {
			return "", fmt.Errorf("name cannot be empty")
		}
		return "", fmt.Errorf("invalid name %q", name)
	}
	return trimmed, nil
}
