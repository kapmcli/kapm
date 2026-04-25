package main

import (
	"flag"
	"os"
	"time"

	"github.com/kapmcli/kapm/internal/hook"
)

func main() {
	agent := flag.String("agent", "", "agent name to attach to hook log records")
	flag.Parse()
	if *agent == "" {
		*agent = os.Getenv("AGENT")
	}
	os.Exit(hook.Handle(os.Stdin, os.Stdout, os.Stderr, time.Now, ".", *agent))
}
