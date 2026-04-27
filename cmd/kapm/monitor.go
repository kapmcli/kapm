package main

import (
	"context"
	"errors"
	"flag"
	"os"

	"github.com/kapmcli/kapm/internal/monitor"
)

var runMonitor = runLogsCommand(
	"monitor",
	"Monitor agent activity metrics.",
	func(fs *flag.FlagSet) {
		fs.Bool("json", false, "output DetailedMetrics as JSON instead of launching TUI")
		fs.String("session", "", "Narrow output to a single session. Merged view when agent is\n\t\t\t\tnot specified; combine with --agent to narrow to one agent. (requires --json)")
		fs.String("agent", "", "Narrow output to sessions owned by this agent. (requires --json)")
	},
	func(ctx context.Context, fs *flag.FlagSet, lf logsFlags) error {
		jsonOut := fs.Lookup("json").Value.(interface{ Get() interface{} }).Get().(bool)
		session := fs.Lookup("session").Value.String()
		agent := fs.Lookup("agent").Value.String()

		if (session != "" || agent != "") && !jsonOut {
			return errors.New("--session and --agent require --json")
		}

		if jsonOut {
			return monitor.RunJSON(ctx, lf.LogsDir, lf.Since, session, agent, os.Stdout)
		}
		return monitor.RunTUI(ctx, lf.LogsDir, lf.Since)
	},
)
