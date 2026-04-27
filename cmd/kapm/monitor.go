package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kapmcli/kapm/internal/monitor"
)

func runMonitor(args []string) error {
	fs := flag.NewFlagSet("monitor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	since, logsDir, targetDir := addLogsFlags(fs)
	jsonOut := fs.Bool("json", false, "output DetailedMetrics as JSON instead of launching TUI")
	session := fs.String("session", "", "Narrow output to a single session. Merged view when agent is\n\t\t\t\tnot specified; combine with --agent to narrow to one agent. (requires --json)")
	agent := fs.String("agent", "", "Narrow output to sessions owned by this agent. (requires --json)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage: kapm monitor [flags]\n\n")
		_, _ = fmt.Fprintln(fs.Output(), "Monitor agent activity metrics.")
		fs.PrintDefaults()
	}

	ok, err := parseLogsCommand(fs, args, "monitor")
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	if (*session != "" || *agent != "") && !*jsonOut {
		return fmt.Errorf("--session and --agent require --json")
	}

	lf, err := resolveLogsFlags(*since, *logsDir, *targetDir)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *jsonOut {
		return monitor.RunJSON(ctx, lf.LogsDir, lf.Since, *session, *agent, os.Stdout)
	}

	return monitor.RunTUI(ctx, lf.LogsDir, lf.Since)
}
