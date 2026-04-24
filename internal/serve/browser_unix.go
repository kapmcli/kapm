//go:build !windows

package serve

import (
	"fmt"
	"os/exec"
)

// openBrowser tries macOS `open` first, then Linux `xdg-open`.
func openBrowser(url string) error {
	for _, bin := range []string{"open", "xdg-open"} {
		if _, err := exec.LookPath(bin); err != nil {
			continue
		}
		if err := exec.Command(bin, url).Start(); err != nil {
			return fmt.Errorf("browser open %q: %w", url, err)
		}
		return nil
	}
	return fmt.Errorf("browser open %q: no opener found (open or xdg-open)", url)
}
