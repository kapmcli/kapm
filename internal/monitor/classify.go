package monitor

import (
	"encoding/json"
	"strings"

	"github.com/kapmcli/kapm/internal/apmconfig"
)

// shellSubAllowlist lists top-level commands whose subcommand is meaningful
// enough to include in the derived bucket key (e.g. "git push", "go test").
// Anything outside the list collapses to its top-level token only.
var shellSubAllowlist = map[string]struct{}{
	"git":     {},
	"go":      {},
	"npm":     {},
	"npx":     {},
	"pnpm":    {},
	"yarn":    {},
	"cargo":   {},
	"docker":  {},
	"kubectl": {},
	"just":    {},
	"make":    {},
	"uv":      {},
	"uvx":     {},
	"pip":     {},
	"python":  {},
	"node":    {},
}

// shellWrapperPrefixes are tokens that carry no semantic category themselves;
// classification skips past them to look at the next real token.
var shellWrapperPrefixes = map[string]struct{}{
	"sudo":  {},
	"env":   {},
	"time":  {},
	"nohup": {},
}

// classifyShell derives a bucket key for a shell invocation suitable for
// per-command metrics aggregation. It returns "shell" when no better split is
// available (empty, unparsable, or wrapper-only input).
//
// Examples (cwd="/ws"):
//
//	"cd /ws && git push -u origin main" -> "shell:git push"
//	"go test ./..."                     -> "shell:go test"
//	"ls -la"                            -> "shell:ls"
//	"FOO=bar cmd"                       -> "shell"  (env-var prefix, no inner token extracted)
//	""                                  -> "shell"
func classifyShell(rawInput json.RawMessage, cwd string) string {
	var in struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(rawInput, &in); err != nil || in.Command == "" {
		return apmconfig.ToolShell
	}
	cmd := stripCdToCwd(in.Command, cwd)
	tokens := strings.Fields(cmd)

	// Skip wrapper prefixes like "sudo", "env", "time".
	// "FOO=bar cmd" -> first token contains '=' and is not a wrapper name,
	// so we bail out to the base key (no meaningful derivation).
	i := 0
	for i < len(tokens) {
		if _, ok := shellWrapperPrefixes[tokens[i]]; ok {
			i++
			continue
		}
		if strings.Contains(tokens[i], "=") {
			return apmconfig.ToolShell
		}
		break
	}
	if i >= len(tokens) {
		return apmconfig.ToolShell
	}

	top := tokens[i]
	if _, ok := shellSubAllowlist[top]; ok && i+1 < len(tokens) {
		sub := tokens[i+1]
		// Ignore flag-only second tokens (e.g. "git --help").
		if !strings.HasPrefix(sub, "-") {
			return apmconfig.ToolShell + ":" + top + " " + sub
		}
	}
	return apmconfig.ToolShell + ":" + top
}

// baseToolName returns the original tool name for a (possibly derived) bucket
// key. For "shell:..." keys this is "shell"; otherwise the key itself.
func baseToolName(key string) string {
	if i := strings.IndexByte(key, ':'); i > 0 {
		return key[:i]
	}
	return key
}
