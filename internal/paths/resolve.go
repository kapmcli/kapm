package paths

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ResolveExecutablePath resolves the absolute path of the kapm executable.
// If executable is empty, it is inferred from os.Args[0] or os.Executable.
func ResolveExecutablePath(executable string) (string, error) {
	if executable == "" {
		invokedPath := os.Args[0]
		if invokedPath == "" {
			detected, err := os.Executable()
			if err != nil {
				return "", fmt.Errorf("determine kapm executable: %w", err)
			}
			executable = detected
		} else if strings.ContainsRune(invokedPath, os.PathSeparator) {
			executable = invokedPath
		} else {
			lookedUp, err := exec.LookPath(invokedPath)
			if err != nil {
				return "", fmt.Errorf("resolve kapm executable %q: %w", invokedPath, err)
			}
			executable = lookedUp
		}
	}
	absPath, err := filepath.Abs(executable)
	if err != nil {
		return "", fmt.Errorf("abs kapm executable %q: %w", executable, err)
	}
	return absPath, nil
}
