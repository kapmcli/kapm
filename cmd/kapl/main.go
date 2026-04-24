package main

import (
	"os"
	"time"

	"github.com/kapmcli/kapm/internal/hook"
)

func main() {
	os.Exit(hook.Handle(os.Stdin, os.Stdout, os.Stderr, time.Now, ".", os.Getenv("AGENT")))
}
