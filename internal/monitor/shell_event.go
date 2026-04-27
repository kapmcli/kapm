package monitor

import (
	"strings"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

// HasShellEvent reports whether the session's timeline contains any shell
// tool invocation (including classified variants like "shell:git").
func HasShellEvent(s SessionDetail) bool {
	for _, e := range s.Timeline {
		if e.Tool == apmconfig.ToolShell || strings.HasPrefix(e.Tool, "shell:") {
			return true
		}
	}
	return false
}
