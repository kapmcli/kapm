//go:build windows

package serve

import (
	"fmt"
	"os/exec"
)

func openBrowser(url string) error {
	if err := exec.Command("cmd", "/c", "start", "", url).Start(); err != nil {
		return fmt.Errorf("browser open %q: %w", url, err)
	}
	return nil
}
