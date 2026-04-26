package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/kapmcli/kapm/internal/fileutil"
	"github.com/kapmcli/kapm/internal/monitor"
	"github.com/kapmcli/kapm/internal/paths"
)

// logsFlags holds the parsed result of the common --since / --logs-dir / --target-dir flags.
type logsFlags struct {
	LogsDir string
	Since   time.Duration
}

// parseLogsCommand parses a command's flags and rejects positional arguments.
func parseLogsCommand(fs *flag.FlagSet, args []string, command string) (bool, error) {
	ok, err := parseFlagSet(fs, args)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	if err := rejectPositionalArgs(fs, command); err != nil {
		return true, err
	}
	return true, nil
}

// addLogsFlags registers the common flags on fs and returns pointers to the flag values.
// Call resolveLogsFlags after fs.Parse(args) succeeds.
func addLogsFlags(fs *flag.FlagSet) (since, logsDir, targetDir *string) {
	since = fs.String("since", "24h", "time window (e.g. 1h, 3d, 1w)")
	logsDir = fs.String("logs-dir", "", "path to logs directory (default: <target-dir>/.kapm/logs)")
	targetDir = fs.String("target-dir", ".", "target directory (default: current directory)")
	return
}

// resolveLogsFlags validates and resolves the common flags into a logsFlags.
func resolveLogsFlags(since, logsDir, targetDir string) (logsFlags, error) {
	td, err := expandTarget(targetDir)
	if err != nil {
		return logsFlags{}, err
	}
	resolved := logsDir
	if resolved == "" {
		resolved = filepath.Join(td, paths.KapmDir, paths.LogsSubdir)
	}
	fileutil.WarnIfKapmSymlink(resolved)
	d, err := monitor.ParseDuration(since)
	if err != nil {
		return logsFlags{}, fmt.Errorf("--since: %w", err)
	}
	return logsFlags{LogsDir: resolved, Since: d}, nil
}
