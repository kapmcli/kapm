package monitor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LoadAll loads sessions + hook logs, merges them, and returns MergedRecords.
// hookLogsDir may be empty or non-existent (sessions-only mode).
// ideBaseDir, when non-empty, loads IDE sessions and appends them.
// cwdFilter limits sessions to those whose cwd starts with the given path;
// empty means no filter (global mode).
// cache may be nil (no caching). Returns a new cache for reuse.
func LoadAll(ctx context.Context, sessionsDir, hookLogsDir, ideBaseDir string, since time.Time, cwdFilter string, cache *SessionCache) ([]MergedRecord, *SessionCache, error) {
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

	if ideBaseDir != "" {
		ideSessions, ideErr := LoadIDESessions(ctx, ideBaseDir, since, cwdFilter)
		if ideErr != nil {
			slog.Warn("load ide sessions", "err", ideErr)
		} else if len(ideSessions) > 0 {
			execIDs := collectExecutionIDs(ideSessions)
			execResults, execErr := LoadIDEExecutions(ctx, ideBaseDir, execIDs)
			if execErr != nil {
				slog.Warn("load ide executions", "err", execErr)
			}
			records = append(records, BuildIDEMergedRecords(ideSessions, execResults)...)
		}
	}

	return records, nextCache, nil
}

// collectExecutionIDs extracts all ExecutionIDs from IDE sessions into a set.
func collectExecutionIDs(sessions []IDEParsedSession) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, s := range sessions {
		for _, id := range s.ExecutionIDs {
			ids[id] = struct{}{}
		}
	}
	return ids
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
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(hookLogsDir, e.Name())
		recs, err := parseHookRecords(path)
		if err != nil {
			slog.Warn("skipped hook log file", "path", path, "err", err)
			continue
		}
		all = append(all, recs...)
	}
	return all, nil
}
