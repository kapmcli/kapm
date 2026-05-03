package monitor

import (
	"strconv"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

// toolFormatter returns a formatted summary for a tool input.
// ok=false means no tool-specific handling applied; caller should use genericSummary.
type toolFormatter func(in toolInput, cwd string) (summary string, ok bool)

var toolFormatters = map[string]toolFormatter{
	apmconfig.ToolRead:  formatReadSummary,
	ActionReadFiles:     formatReadSummary,
	apmconfig.ToolGrep:  formatGrepSummary,
	apmconfig.ToolGlob:  formatGlobSummary,
	apmconfig.ToolShell: formatShellSummary,
	apmconfig.ToolWrite: formatWriteSummary,
}

func formatReadSummary(in toolInput, _ string) (string, bool) {
	if len(in.Paths) > 0 && in.Paths[0] != "" {
		if len(in.Paths) > 1 {
			return in.Paths[0] + " (+" + strconv.Itoa(len(in.Paths)-1) + " more)", true
		}
		return in.Paths[0], true
	}
	if len(in.Operations) == 0 {
		return "", false
	}
	first := in.Operations[0]
	path := first.Path
	if path == "" {
		path = first.FilePath
	}
	if path == "" && len(first.ImagePaths) > 0 {
		path = first.ImagePaths[0]
	}
	if path == "" {
		return "", false
	}
	if first.Offset > 0 && first.Limit > 0 {
		return path + ":" + strconv.Itoa(first.Offset) + "-" + strconv.Itoa(first.Offset+first.Limit), true
	}
	if first.Limit > 0 {
		return path + ":1-" + strconv.Itoa(first.Limit+1), true
	}
	if first.Offset > 0 {
		return path + ":" + strconv.Itoa(first.Offset) + "+", true
	}
	if len(in.Operations) > 1 {
		return path + " (+" + strconv.Itoa(len(in.Operations)-1) + " more)", true
	}
	return path, true
}

func formatGrepSummary(in toolInput, _ string) (string, bool) {
	if in.Pattern == "" {
		return "", false
	}
	if in.Path != "" {
		return `"` + in.Pattern + `" in ` + in.Path, true
	}
	return `"` + in.Pattern + `"`, true
}

func formatGlobSummary(in toolInput, _ string) (string, bool) {
	if in.Pattern == "" {
		return "", false
	}
	if in.Path != "" {
		return in.Pattern + " in " + in.Path, true
	}
	return in.Pattern, true
}

func formatShellSummary(in toolInput, cwd string) (string, bool) {
	if in.Command == "" {
		return "", false
	}
	return stripCdToCwd(in.Command, cwd), true
}

func formatWriteSummary(in toolInput, _ string) (string, bool) {
	if in.Path == "" {
		return "", false
	}
	if in.Command != "" {
		return in.Command + " " + in.Path, true
	}
	return in.Path, true
}
