package monitor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LoadAll loads sessions + hook logs, merges them, and returns MergedRecords.
// hookLogsDir may be empty or non-existent (sessions-only mode).
// cwdFilter limits sessions to those whose cwd starts with the given path;
// empty means no filter (global mode).
// cache may be nil (no caching). Returns a new cache for reuse.
func LoadAll(ctx context.Context, sessionsDir, hookLogsDir string, since time.Time, cwdFilter string, cache *SessionCache) ([]MergedRecord, *SessionCache, error) {
	sessions, nextCache, err := LoadSessions(ctx, sessionsDir, since, cwdFilter, cache)
	if err != nil {
		return nil, nil, fmt.Errorf("load sessions: %w", err)
	}

	var hookLogs []HookRecord
	if hookLogsDir != "" {
		hookLogs, err = loadAllHookRecords(ctx, hookLogsDir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("load hook logs: %w", err)
		}
	}

	records := MergeSessions(sessions, hookLogs)
	if records == nil {
		records = []MergedRecord{}
	}
	return records, nextCache, nil
}

// loadAllHookRecords reads all .jsonl files in hookLogsDir and returns
// parsed HookRecords. Honors ctx cancellation.
func loadAllHookRecords(ctx context.Context, hookLogsDir string) ([]HookRecord, error) {
	entries, err := os.ReadDir(hookLogsDir)
	if err != nil {
		return nil, err
	}

	var all []HookRecord
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(hookLogsDir, e.Name())
		recs, err := parseHookRecords(path)
		if err != nil {
			continue // skip missing or unreadable hook log files
		}
		all = append(all, recs...)
	}
	return all, nil
}
