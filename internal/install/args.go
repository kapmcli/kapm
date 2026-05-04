package install

import "strings"

// SplitArgs separates kapm install-only flags from arguments forwarded to APM.
func SplitArgs(args []string) (force bool, targetDir string, installArgs []string) {
	installArgs = make([]string, 0, len(args))
	targetDir = "."

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--sync-force" {
			force = true
			continue
		}
		if arg == "--target-dir" {
			if i+1 < len(args) {
				i++
				targetDir = args[i]
			}
			continue
		}
		if v, ok := strings.CutPrefix(arg, "--target-dir="); ok {
			targetDir = v
			continue
		}
		installArgs = append(installArgs, arg)
	}

	return force, targetDir, installArgs
}
