// Package idehookhandler records minimal Kiro IDE hook events for monitoring.
package idehookhandler

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"github.com/kapmcli/kapm/internal/fileutil"
	"github.com/kapmcli/kapm/internal/paths"
)

const ideHookLogFile = "events.jsonl"

// Options configures IDE hook handling.
type Options struct {
	Root   string
	Event  string
	Agent  string
	Err    io.Writer
	Now    func() time.Time
	Getenv func(string) string
}

type record struct {
	Ts    string `json:"ts"`
	Event string `json:"event,omitempty"`
	Agent string `json:"agent,omitempty"`
	Cwd   string `json:"cwd,omitempty"`
}

// Handle appends one minimal JSONL record to .kapm/logs/ide/events.jsonl.
func Handle(opts Options) (err error) {
	if opts.Root == "" {
		opts.Root = "."
	}
	if opts.Err == nil {
		opts.Err = os.Stderr
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}

	agent := opts.Agent
	if agent == "" {
		agent = opts.Getenv("AGENT")
	}
	if agent != "" {
		if _, err := apmconfig.ValidateIdentifier(agent); err != nil {
			_, _ = fmt.Fprintf(opts.Err, "ide-hook-handler: invalid agent name %q, clearing\n", agent)
			agent = ""
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		slog.Warn("ide-hook-handler: get cwd", "err", err)
	}
	rec := record{
		Ts:    opts.Now().UTC().Format(time.RFC3339Nano),
		Event: opts.Event,
		Agent: agent,
		Cwd:   cwd,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}
	line = append(line, '\n')

	logDir := filepath.Join(opts.Root, paths.KapmDir, paths.LogsSubdir, paths.IDESubdir)
	if err := fileutil.RefuseSymlinkPathUnder(opts.Root, logDir); err != nil {
		return fmt.Errorf("%w, refusing to write logs", err)
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return fmt.Errorf("mkdir %q: %w", logDir, err)
	}
	if err := fileutil.RefuseSymlinkPathUnder(opts.Root, logDir); err != nil {
		return fmt.Errorf("%w, refusing to write logs", err)
	}
	logPath := filepath.Join(logDir, ideHookLogFile)
	if err := fileutil.RefuseSymlinkPathUnder(opts.Root, logPath); err != nil {
		return fmt.Errorf("%w, refusing to write logs", err)
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open %q: %w", logPath, err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close %q: %w", logPath, closeErr)
		}
	}()
	// FlockExclusive is a no-op on Windows; see fileutil/flock_windows.go.
	if err := fileutil.FlockExclusive(f); err != nil {
		return fmt.Errorf("flock %q: %w", logPath, err)
	}
	defer fileutil.FlockUnlock(f)
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("write %q: %w", logPath, err)
	}
	return nil
}
