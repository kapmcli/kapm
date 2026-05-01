package main

import (
	"context"
	"errors"
	"flag"
	"os"

	"github.com/kapmcli/kapm/internal/monitor"
)

var runMonitor = func() func([]string) error {
	var monitorJSON bool
	return runLogsCommand(
		"monitor",
		"Monitor agent activity metrics.",
		func(fs *flag.FlagSet) {
			fs.BoolVar(&monitorJSON, "json", false, "output DetailedMetrics as JSON instead of launching TUI")
			fs.String("session", "", "Narrow output to a single session. Merged view when agent is\n\t\t\t\tnot specified; combine with --agent to narrow to one agent. (requires --json)")
			fs.String("agent", "", "Narrow output to sessions owned by this agent. (requires --json)")
		},
		func(ctx context.Context, fs *flag.FlagSet, lf logsFlags) error {
			session := fs.Lookup("session").Value.String()
			agent := fs.Lookup("agent").Value.String()

			if (session != "" || agent != "") && !monitorJSON {
				return errors.New("--session and --agent require --json")
			}

			if monitorJSON {
				return monitor.RunJSON(ctx, lf.SessionsDir, lf.LogsDir, lf.IDESessionsDir, lf.CwdFilter, lf.SQLiteDBPath, lf.Since, session, agent, os.Stdout)
			}
			return monitor.RunTUI(ctx, lf.SessionsDir, lf.LogsDir, lf.IDESessionsDir, lf.CwdFilter, lf.SQLiteDBPath, lf.Since)
		},
	)
}()
