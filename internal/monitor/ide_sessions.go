package monitor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// IDEParsedSession is the parsed result of one IDE session.
type IDEParsedSession struct {
	SessionID          string
	Title              string
	WorkspaceDirectory string
	CreatedAt          time.Time
	ExecutionIDs       []string
	Prompts            int // number of user turns
	PromptTexts        []string
}

// LoadIDESessions scans ideBaseDir/workspace-sessions, filters by since and
// cwdFilter, and returns parsed sessions with their executionId lists.
// Returns nil, nil if ideBaseDir is empty or does not exist.
func LoadIDESessions(ctx context.Context, ideBaseDir string, since time.Time, cwdFilter string) ([]IDEParsedSession, error) {
	if ideBaseDir == "" {
		return nil, nil
	}
	wsDir := filepath.Join(ideBaseDir, "workspace-sessions")
	wsDirs, err := os.ReadDir(wsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []IDEParsedSession
	for _, d := range wsDirs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !d.IsDir() {
			continue
		}
		workspacePath, err := decodeWorkspacePath(d.Name())
		if err != nil {
			slog.Warn("ide: skip workspace dir: base64 decode failed", "dir", d.Name(), "err", err)
			continue
		}
		if cwdFilter != "" && !strings.HasPrefix(workspacePath, cwdFilter) {
			continue
		}
		parsed, err := loadWorkspaceSessions(ctx, filepath.Join(wsDir, d.Name()), workspacePath, since)
		if err != nil {
			slog.Warn("ide: skip workspace", "path", workspacePath, "err", err)
			continue
		}
		sessions = append(sessions, parsed...)
	}
	return sessions, nil
}

func decodeWorkspacePath(name string) (string, error) {
	// URL-safe base64, no padding; `_` at end may represent `=` padding
	// Replace trailing `_` used as padding with `=`
	fixed := strings.TrimRight(name, "_")
	padded := fixed
	switch len(fixed) % 4 {
	case 2:
		padded = fixed + "=="
	case 3:
		padded = fixed + "="
	}
	// Use RawURLEncoding (no padding) on the trimmed string
	b, err := base64.RawURLEncoding.DecodeString(fixed)
	if err != nil {
		// Try with padding restored
		b, err = base64.URLEncoding.DecodeString(padded)
		if err != nil {
			return "", err
		}
	}
	return string(b), nil
}

func loadWorkspaceSessions(ctx context.Context, dir, workspacePath string, since time.Time) ([]IDEParsedSession, error) {
	data, err := os.ReadFile(filepath.Join(dir, "sessions.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var entries []IDESessionEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}

	var result []IDEParsedSession
	for _, e := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		createdAt := parseUnixMillis(e.DateCreated)
		if !createdAt.IsZero() && createdAt.Before(since) {
			continue
		}
		execIDs, promptTexts, err := loadExecutionIDs(filepath.Join(dir, e.SessionID+".json"))
		if err != nil {
			slog.Warn("ide: skip session file", "sessionId", e.SessionID, "err", err)
			continue
		}
		result = append(result, IDEParsedSession{
			SessionID:          e.SessionID,
			Title:              e.Title,
			WorkspaceDirectory: workspacePath,
			CreatedAt:          createdAt,
			ExecutionIDs:       execIDs,
			Prompts:            len(promptTexts),
			PromptTexts:        promptTexts,
		})
	}
	return result, nil
}

func parseUnixMillis(s string) time.Time {
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func loadExecutionIDs(path string) ([]string, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var history IDESessionHistory
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, nil, err
	}
	var ids []string
	var promptTexts []string
	for _, h := range history.History {
		if h.ExecutionID != "" {
			ids = append(ids, h.ExecutionID)
		}
		if h.Message.Role == "user" {
			promptTexts = append(promptTexts, extractMessageText(h.Message.Content))
		}
	}
	return ids, promptTexts, nil
}

// extractMessageText extracts plain text from an IDEMessage Content field,
// which may be a JSON string or a JSON array of content items.
func extractMessageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string first.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	// Try array of {type, text} items.
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &items) == nil {
		for _, item := range items {
			if item.Type == "text" && item.Text != "" {
				return item.Text
			}
		}
	}
	return ""
}
