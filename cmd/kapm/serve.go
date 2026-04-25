package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kapmcli/kapm/internal/serve"
)

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	port := fs.Int("port", 9090, "HTTP port")
	open := fs.Bool("open", false, "open browser automatically")
	since, logsDir, targetDir := addLogsFlags(fs)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage: kapm serve [flags]\n\n")
		_, _ = fmt.Fprintln(fs.Output(), "Serve the kapm WebUI dashboard over HTTP.")
		fs.PrintDefaults()
	}

	ok, err := parseFlagSet(fs, args)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := rejectPositionalArgs(fs, "serve"); err != nil {
		return err
	}

	lf, err := resolveLogsFlags(*since, *logsDir, *targetDir)
	if err != nil {
		return err
	}

	srv := serve.New(serve.Options{Port: *port, LogsDir: lf.LogsDir, Since: lf.Since})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	url := fmt.Sprintf("http://%s/", srv.Addr())
	_, _ = fmt.Fprintf(os.Stdout, "kapm serve listening on %s\n", url)
	if *open {
		if err := serve.OpenBrowser(url); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}

	return srv.Run(ctx)
}
