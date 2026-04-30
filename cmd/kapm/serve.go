package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/kapmcli/kapm/internal/serve"
)

var (
	servePort int
	serveOpen bool
)

var runServe = runLogsCommand(
	"serve",
	"Serve the kapm WebUI dashboard over HTTP.",
	func(fs *flag.FlagSet) {
		fs.IntVar(&servePort, "port", 9090, "HTTP port")
		fs.BoolVar(&serveOpen, "open", false, "open browser automatically")
	},
	func(ctx context.Context, fs *flag.FlagSet, lf logsFlags) error {
		srv := serve.New(serve.Options{Port: servePort, SessionsDir: lf.SessionsDir, LogsDir: lf.LogsDir, IDEBaseDir: lf.IDESessionsDir, CwdFilter: lf.CwdFilter, Since: lf.Since, SQLiteDBPath: lf.SQLiteDBPath})

		url := fmt.Sprintf("http://%s/", srv.Addr())
		_, _ = fmt.Fprintf(os.Stdout, "kapm serve listening on %s\n", url)
		if serveOpen {
			if err := serve.OpenBrowser(url); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}

		return srv.Run(ctx)
	},
)
