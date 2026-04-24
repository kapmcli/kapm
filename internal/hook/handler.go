package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"github.com/kapmcli/kapm/internal/fileutil"
	"github.com/kapmcli/kapm/internal/paths"
)

const rotateMinAge = 24 * time.Hour

const maxHookEvent = 10 << 20 // 10 MiB

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func isSafeSessionID(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	if strings.ContainsAny(s, `/\`) {
		return false
	}
	if strings.Contains(s, "..") {
		return false
	}
	return sessionIDPattern.MatchString(s)
}

// hookEvent is the shape of a Kiro hook event received on stdin.
type hookEvent struct {
	HookEventName     string          `json:"hook_event_name"`
	SessionID         string          `json:"session_id"`
	Cwd               string          `json:"cwd"`
	ToolName          string          `json:"tool_name"`
	ToolInput         json.RawMessage `json:"tool_input"`
	ToolResponse      json.RawMessage `json:"tool_response"`
	Prompt            string          `json:"prompt"`
	AssistantResponse json.RawMessage `json:"assistant_response"`
}

// record is the JSONL line written to the log file.
// Fixed key order via struct field order + omitempty.
type record struct {
	Ts                string          `json:"ts"`
	Agent             string          `json:"agent,omitempty"`
	Session           string          `json:"session,omitempty"`
	Event             string          `json:"event,omitempty"`
	Tool              string          `json:"tool,omitempty"`
	ToolInput         json.RawMessage `json:"tool_input,omitempty"`
	ToolResponse      json.RawMessage `json:"tool_response,omitempty"`
	AssistantResponse json.RawMessage `json:"assistant_response,omitempty"`
	Prompt            string          `json:"prompt,omitempty"`
	Cwd               string          `json:"cwd,omitempty"`
}

// Handle reads a Kiro hook event from in, appends a JSONL record to logs under rootDir,
// and returns 0 always. Never writes to stdout. Writes diagnostics to stderr on error.
func Handle(in io.Reader, stdout, stderr io.Writer, now func() time.Time, rootDir, agent string) (exitCode int) {
	defer func() {
		if r := recover(); r != nil {
			_, _ = fmt.Fprintf(stderr, "hook-handler: recovered panic: %v\n", r)
			exitCode = 0
		}
	}()

	limited := io.LimitReader(in, maxHookEvent+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "hook-handler: read stdin: %v\n", err)
		return 0
	}
	if int64(len(data)) > maxHookEvent {
		slog.Warn("hook event rejected: too large",
			"size_bytes", len(data),
			"max_bytes", maxHookEvent,
		)
		_, _ = fmt.Fprintf(stderr, "hook-handler: hook event too large (>%d bytes)\n", maxHookEvent)
		return 0
	}

	var ev hookEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		_, _ = fmt.Fprintf(stderr, "hook-handler: parse json: %v\n", err)
		return 0
	}

	sessionID := ev.SessionID
	if !isSafeSessionID(sessionID) {
		fallback := fmt.Sprintf("unknown-%d", now().UnixNano())
		_, _ = fmt.Fprintf(stderr, "hook-handler: invalid session_id %q, using %s\n", sessionID, fallback)
		sessionID = fallback
	}

	if agent != "" {
		if _, err := apmconfig.ValidateIdentifier(agent); err != nil {
			_, _ = fmt.Fprintf(stderr, "hook-handler: invalid agent name %q, clearing\n", agent)
			agent = ""
		}
	}

	rec := record{
		Ts:                now().UTC().Format(time.RFC3339Nano),
		Agent:             agent,
		Session:           sessionID,
		Event:             ev.HookEventName,
		Tool:              ev.ToolName,
		ToolInput:         ev.ToolInput,
		ToolResponse:      ev.ToolResponse,
		AssistantResponse: ev.AssistantResponse,
		Prompt:            ev.Prompt,
		Cwd:               ev.Cwd,
	}

	line, err := json.Marshal(rec)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "hook-handler: marshal record: %v\n", err)
		return 0
	}
	line = append(line, '\n')

	logDir := filepath.Join(rootDir, paths.KiroDir, paths.LogsSubdir)
	kiroDir := filepath.Join(rootDir, paths.KiroDir)
	if isLink, err := fileutil.IsSymlinkPath(kiroDir); err == nil && isLink {
		_, _ = fmt.Fprintf(stderr, "hook-handler: %q is a symlink, refusing to write logs\n", kiroDir)
		return 0
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		_, _ = fmt.Fprintf(stderr, "hook-handler: mkdir %q: %v\n", logDir, err)
		return 0
	}

	logPath := filepath.Join(logDir, sessionID+".jsonl")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "hook-handler: open %q: %v\n", logPath, err)
		return 0
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			_, _ = fmt.Fprintf(stderr, "hook-handler: close %q: %v\n", logPath, cerr)
		}
	}()

	if err := flockExclusive(f); err != nil {
		_, _ = fmt.Fprintf(stderr, "hook-handler: flock %q: %v\n", logPath, err)
		return 0
	}
	defer flockUnlock(f)

	if _, err := f.Write(line); err != nil {
		_, _ = fmt.Fprintf(stderr, "hook-handler: write %q: %v\n", logPath, err)
	}

	if ev.HookEventName == apmconfig.EventAgentSpawn {
		rotate(logDir, sessionID, stderr, rotateMinAge)
	}
	return 0
}
