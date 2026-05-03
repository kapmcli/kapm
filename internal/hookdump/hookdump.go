// Package hookdump records raw hook command input for integration debugging.
package hookdump

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kapmcli/kapm/internal/fileutil"
	"github.com/kapmcli/kapm/internal/paths"
)

const maxInput = 10 << 20 // 10 MiB
const stdinReadTimeout = 200 * time.Millisecond

// Options configures a hook input dump.
type Options struct {
	Root  string
	Event string
	Agent string
	In    io.Reader
	Out   io.Writer
	Err   io.Writer
	Now   func() time.Time
}

type record struct {
	Ts             string            `json:"ts"`
	Event          string            `json:"event,omitempty"`
	Agent          string            `json:"agent,omitempty"`
	Cwd            string            `json:"cwd,omitempty"`
	Stdin          string            `json:"stdin"`
	StdinBytes     int               `json:"stdin_bytes"`
	StdinReadTimed bool              `json:"stdin_read_timed_out,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	EnvKeys        []string          `json:"env_keys,omitempty"`
}

// Dump appends one JSONL record to .kapm/logs/hook-input.jsonl.
func Dump(opts Options) error {
	if opts.Root == "" {
		opts.Root = "."
	}
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Err == nil {
		opts.Err = os.Stderr
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	data, timedOut, err := readStdinWithTimeout(opts.In, stdinReadTimeout)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	cwd, _ := os.Getwd()
	rec := record{
		Ts:             opts.Now().UTC().Format(time.RFC3339Nano),
		Event:          opts.Event,
		Agent:          opts.Agent,
		Cwd:            cwd,
		Stdin:          string(data),
		StdinBytes:     len(data),
		StdinReadTimed: timedOut,
		Env:            selectedEnv(),
		EnvKeys:        envKeys(),
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal dump record: %w", err)
	}
	line = append(line, '\n')

	logDir := filepath.Join(opts.Root, paths.KapmDir, paths.LogsSubdir)
	kapmDir := filepath.Join(opts.Root, paths.KapmDir)
	if isLink, err := fileutil.IsSymlinkPath(kapmDir); err == nil && isLink {
		return fmt.Errorf("%q is a symlink, refusing to write logs", kapmDir)
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return fmt.Errorf("mkdir %q: %w", logDir, err)
	}
	logPath := filepath.Join(logDir, "hook-input.jsonl")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open %q: %w", logPath, err)
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("write %q: %w", logPath, err)
	}
	_, _ = fmt.Fprintf(opts.Out, "%s\n", logPath)
	return nil
}

func readStdinWithTimeout(in io.Reader, timeout time.Duration) ([]byte, bool, error) {
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		limited := io.LimitReader(in, maxInput+1)
		data, err := io.ReadAll(limited)
		if len(data) > maxInput {
			data = data[:maxInput]
		}
		ch <- result{data: data, err: err}
	}()

	select {
	case res := <-ch:
		return res.data, false, res.err
	case <-time.After(timeout):
		return nil, true, nil
	}
}

func selectedEnv() map[string]string {
	env := map[string]string{}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if key == "USER_PROMPT" || strings.HasPrefix(key, "KIRO_") {
			env[key] = value
		}
	}
	return env
}

func envKeys() []string {
	keys := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}
