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

// addLogsFlags registers the common flags on fs and returns pointers to the flag values.
// Call resolveLogsFlags after fs.Parse(args) succeeds.
func addLogsFlags(fs *flag.FlagSet) (since, logsDir, targetDir *string) {
	since = fs.String("since", "24h", "time window (e.g. 1h, 3d, 1w)")
	logsDir = fs.String("logs-dir", "", "path to logs directory (default: <target-dir>/.kiro/logs)")
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
		resolved = filepath.Join(td, paths.KiroDir, paths.LogsSubdir)
	}
	fileutil.WarnIfKiroSymlink(resolved)
	d, err := monitor.ParseDuration(since)
	if err != nil {
		return logsFlags{}, fmt.Errorf("--since: %w", err)
	}
	return logsFlags{LogsDir: resolved, Since: d}, nil
}
