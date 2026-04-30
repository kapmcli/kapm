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
	"sync"
	"time"
)

// SQLiteCache caches v1 SQLite sessions, invalidated by file mtime.
type SQLiteCache struct {
	mu       sync.Mutex
	mtime    time.Time
	sessions []ParsedSession
}

// NewSQLiteCache returns an initialized *SQLiteCache.
func NewSQLiteCache() *SQLiteCache { return &SQLiteCache{} }

// Load returns cached v1 sessions, re-reading only when the DB file mtime changes.
// Returns ALL sessions (unfiltered). Caller applies since/cwdFilter post-cache.
func (sc *SQLiteCache) Load(ctx context.Context, dbPath string) ([]ParsedSession, error) {
	if dbPath == "" {
		return nil, nil
	}
	info, err := os.Stat(dbPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat sqlite db: %w", err)
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()
	if info.ModTime().Equal(sc.mtime) {
		return sc.sessions, nil
	}
	sessions, err := LoadSessionsSQLite(ctx, dbPath, time.Time{}, "")
	if err != nil {
		return nil, err
	}
	sc.mtime = info.ModTime()
	sc.sessions = sessions
	return sessions, nil
}

// LoadAll loads sessions + hook logs, merges them, and returns MergedRecords.
// hookLogsDir may be empty or non-existent (sessions-only mode).
// ideBaseDir, when non-empty, loads IDE sessions and appends them.
// sqliteDBPath, when non-empty, loads v1 SQLite sessions and merges them (v2 priority).
// cwdFilter limits sessions to those whose cwd starts with the given path;
// empty means no filter (global mode).
// cache and sqliteCache may be nil (no caching). Returns a new cache for reuse.
func LoadAll(ctx context.Context, sessionsDir, hookLogsDir, ideBaseDir, sqliteDBPath string, since time.Time, cwdFilter string, cache *SessionCache, sqliteCache *SQLiteCache) ([]MergedRecord, *SessionCache, error) {
	v2Sessions, nextCache, err := LoadSessions(ctx, sessionsDir, since, cwdFilter, cache)
	if err != nil {
		return nil, nil, fmt.Errorf("load sessions: %w", err)
	}

	var allSessions []ParsedSession
	allSessions = append(allSessions, v2Sessions...)

	if sqliteDBPath != "" && sqliteCache != nil {
		allV1, err := sqliteCache.Load(ctx, sqliteDBPath)
		if err != nil {
			return nil, nil, fmt.Errorf("load sqlite sessions: %w", err)
		}
		var v1Filtered []ParsedSession
		for _, s := range allV1 {
			if !since.IsZero() && time.Time(s.Meta.UpdatedAt).Before(since) {
				continue
			}
			if cwdFilter != "" && !strings.HasPrefix(s.Meta.Cwd, cwdFilter) {
				continue
			}
			v1Filtered = append(v1Filtered, s)
		}
		v2IDs := make(map[string]struct{})
		for _, s := range v2Sessions {
			v2IDs[s.Meta.SessionID] = struct{}{}
		}
		for _, s := range v1Filtered {
			if _, dup := v2IDs[s.Meta.SessionID]; !dup {
				allSessions = append(allSessions, s)
			}
		}
	}

	var hookLogs []HookRecord
	if hookLogsDir != "" {
		hookLogs, err = loadAllHookRecords(ctx, hookLogsDir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("load hook logs: %w", err)
		}
	}

	records := MergeSessions(allSessions, hookLogs)
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
