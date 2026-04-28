package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/kapmcli/kapm/internal/serve"
)

var runServe = runLogsCommand(
	"serve",
	"Serve the kapm WebUI dashboard over HTTP.",
	func(fs *flag.FlagSet) {
		fs.Int("port", 9090, "HTTP port")
		fs.Bool("open", false, "open browser automatically")
	},
	func(ctx context.Context, fs *flag.FlagSet, lf logsFlags) error {
		port := fs.Lookup("port").Value.(interface{ Get() interface{} }).Get().(int)
		open := fs.Lookup("open").Value.(interface{ Get() interface{} }).Get().(bool)

		srv := serve.New(serve.Options{Port: port, SessionsDir: lf.SessionsDir, LogsDir: lf.LogsDir, CwdFilter: lf.CwdFilter, Since: lf.Since})

		url := fmt.Sprintf("http://%s/", srv.Addr())
		_, _ = fmt.Fprintf(os.Stdout, "kapm serve listening on %s\n", url)
		if open {
			if err := serve.OpenBrowser(url); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}

		return srv.Run(ctx)
	},
)
