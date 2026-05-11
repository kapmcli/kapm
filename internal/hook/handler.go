package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"github.com/kapmcli/kapm/internal/fileutil"
	"github.com/kapmcli/kapm/internal/paths"
)

const maxHookEvent = 10 << 20 // 10 MiB

const unknownSessionID = "unknown"

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// HandlerInputOptions configures hook-handler stdin normalization.
type HandlerInputOptions struct {
	Stdin     io.Reader
	Agent     string
	Event     string
	SessionID string
	Tool      string
	Getenv    func(string) string
}

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
type record struct {
	Ts              string `json:"ts"`
	Session         string `json:"session,omitempty"`
	Event           string `json:"event,omitempty"`
	Agent           string `json:"agent,omitempty"`
	Tool            string `json:"tool,omitempty"`
	ShellExitStatus string `json:"shell_exit_status,omitempty"`
}

// extractShellExitStatus extracts exit_status from tool_response.items[].Json.exit_status.
func extractShellExitStatus(raw json.RawMessage) string {
	var resp struct {
		Items []struct {
			Json struct {
				ExitStatus string `json:"exit_status"`
			} `json:"Json"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ""
	}
	for _, item := range resp.Items {
		if item.Json.ExitStatus != "" {
			return item.Json.ExitStatus
		}
	}
	return ""
}

func reportHookErr(stderr io.Writer, op string, err error) {
	_, _ = fmt.Fprintf(stderr, "hook-handler: %s: %v\n", op, err)
}

// PrepareHandlerInput resolves the agent name and synthesizes a minimal hook
// event when a hook host invokes hook-handler with fallback flags and empty stdin.
func PrepareHandlerInput(opts HandlerInputOptions) (io.Reader, string, error) {
	agentName := opts.Agent
	if agentName == "" && opts.Getenv != nil {
		agentName = opts.Getenv("AGENT")
	}
	if opts.Event == "" {
		return opts.Stdin, agentName, nil
	}

	data, err := io.ReadAll(opts.Stdin)
	if err != nil {
		return nil, "", err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		fallback := map[string]string{
			"hook_event_name": opts.Event,
			"session_id":      opts.SessionID,
			"tool_name":       opts.Tool,
		}
		data, err = json.Marshal(fallback)
		if err != nil {
			return nil, "", err
		}
	}
	return bytes.NewReader(data), agentName, nil
}

// Handle reads a Kiro hook event from in, appends a JSONL record to logs under rootDir,
// and returns 0 always. Errors are intentionally suppressed (exit code 0) because hooks
// must never block the agent — a failing hook must not prevent the agent from continuing.
// Diagnostics are written to stderr; stdout is never used.
func Handle(in io.Reader, stdout, stderr io.Writer, now func() time.Time, rootDir, agent string) (exitCode int) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("hook-handler recovered panic", "panic", r)
			slog.Debug("hook-handler panic stack", "stack", string(debug.Stack()))
			exitCode = 0
		}
	}()

	limited := io.LimitReader(in, maxHookEvent+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		reportHookErr(stderr, "read stdin", err)
		slog.Warn("hook handler error", "op", "read stdin", "err", err)
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
		reportHookErr(stderr, "parse json", err)
		slog.Warn("hook handler error", "op", "parse json", "err", err)
		return 0
	}

	sessionID := ev.SessionID
	if !isSafeSessionID(sessionID) {
		_, _ = fmt.Fprintf(stderr, "hook-handler: invalid session_id %q, using %s\n", sessionID, unknownSessionID)
		sessionID = unknownSessionID
	}

	if agent != "" {
		if _, err := apmconfig.ValidateIdentifier(agent); err != nil {
			_, _ = fmt.Fprintf(stderr, "hook-handler: invalid agent name %q, clearing\n", agent)
			agent = ""
		}
	}

	if ev.HookEventName == apmconfig.EventUserPromptSubmit {
		return 0
	}

	rec := record{
		Ts:      now().UTC().Format(time.RFC3339Nano),
		Agent:   agent,
		Session: sessionID,
		Event:   ev.HookEventName,
		Tool:    ev.ToolName,
	}

	if ev.HookEventName == apmconfig.EventPostToolUse && ev.ToolName == "shell" {
		rec.ShellExitStatus = extractShellExitStatus(ev.ToolResponse)
	}

	line, err := json.Marshal(rec)
	if err != nil {
		reportHookErr(stderr, "marshal record", err)
		slog.Warn("hook handler error", "op", "marshal record", "err", err)
		return 0
	}
	line = append(line, '\n')

	logDir := filepath.Join(rootDir, paths.KapmDir, paths.LogsSubdir, paths.CLISubdir)
	f, err := fileutil.OpenSafeLogFile(rootDir, logDir, sessionID+".jsonl")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "hook-handler: %v, refusing to write logs\n", err)
		return 0
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			reportHookErr(stderr, "close log", cerr)
			slog.Warn("hook handler error", "op", "close log", "err", cerr)
		}
	}()
	defer fileutil.FlockUnlock(f)

	if _, err := f.Write(line); err != nil {
		reportHookErr(stderr, fmt.Sprintf("write %q", f.Name()), err)
		slog.Warn("hook handler error", "op", "write log", "err", err)
	}

	return 0
}
