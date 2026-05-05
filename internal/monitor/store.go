package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kapmcli/kapm/internal/paths"
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
			ideRecords := BuildIDEMergedRecords(ideSessions, execResults)
			if hookLogsDir != "" {
				ideHooks, hookErr := loadAllIDEHookRecords(ctx, hookLogsDir)
				if hookErr != nil {
					slog.Warn("load ide hook records", "err", hookErr)
				} else {
					ideRecords = AppendIDEHookRecords(ideRecords, ideSessions, ideHooks)
				}
			}
			records = append(records, ideRecords...)
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

// loadAllHookRecords reads CLI hook logs. Honors ctx cancellation.
func loadAllHookRecords(ctx context.Context, hookLogsDir string) ([]HookRecord, error) {
	cli, err := loadHookRecordsFromDir(ctx, filepath.Join(hookLogsDir, paths.CLISubdir))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	legacy, err := loadLegacyRootHookRecords(ctx, hookLogsDir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return dedupeHookRecords(append(cli, legacy...)), nil
}

func dedupeHookRecords(records []HookRecord) []HookRecord {
	if len(records) < 2 {
		return records
	}
	seen := make(map[string]struct{}, len(records))
	out := records[:0]
	for _, rec := range records {
		key := hookRecordKey(rec)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, rec)
	}
	sortHookRecords(out)
	return out
}

func sortHookRecords(records []HookRecord) {
	slices.SortStableFunc(records, func(a, b HookRecord) int {
		if a.Session != b.Session {
			return strings.Compare(a.Session, b.Session)
		}
		if c := a.Ts.Compare(b.Ts); c != 0 {
			return c
		}
		if a.Event != b.Event {
			return strings.Compare(a.Event, b.Event)
		}
		if a.Tool != b.Tool {
			return strings.Compare(a.Tool, b.Tool)
		}
		return strings.Compare(a.Agent, b.Agent)
	})
}

func hookRecordKey(rec HookRecord) string {
	return rec.Ts.UTC().Format(time.RFC3339Nano) + "\x00" +
		rec.Session + "\x00" +
		rec.Event + "\x00" +
		rec.Agent + "\x00" +
		rec.Tool + "\x00" +
		rec.Cwd + "\x00" +
		rec.ShellExitStatus
}

func loadLegacyRootHookRecords(ctx context.Context, hookLogsDir string) ([]HookRecord, error) {
	entries, err := os.ReadDir(hookLogsDir)
	if err != nil {
		return nil, err
	}

	var all []HookRecord
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.Name() == "hook-input.jsonl" {
			continue
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}

		path := filepath.Join(hookLogsDir, e.Name())
		recs, err := parseHookRecords(path)
		if err != nil {
			slog.Warn("skipped hook log file", "path", path, "err", err)
			continue
		}

		legacySession := strings.TrimSuffix(e.Name(), ".jsonl")
		for _, rec := range recs {
			if rec.Session == legacySession {
				all = append(all, rec)
			}
		}
	}
	return all, nil
}

func loadAllIDEHookRecords(ctx context.Context, hookLogsDir string) ([]HookRecord, error) {
	var records []HookRecord
	current, err := loadHookRecordsFromDir(ctx, filepath.Join(hookLogsDir, paths.IDESubdir))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	} else {
		records = append(records, current...)
	}
	legacy, err := loadLegacyIDEHookInputRecords(ctx, hookLogsDir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	} else {
		records = append(records, legacy...)
	}
	return dedupeHookRecords(records), nil
}

type legacyIDEHookInputJSON struct {
	Ts    time.Time `json:"ts"`
	Event string    `json:"event,omitempty"`
	Agent string    `json:"agent,omitempty"`
	Cwd   string    `json:"cwd,omitempty"`
}

func loadLegacyIDEHookInputRecords(ctx context.Context, hookLogsDir string) ([]HookRecord, error) {
	path := filepath.Join(hookLogsDir, "hook-input.jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var records []HookRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 20*1024*1024)
	var parseFailed int
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw legacyIDEHookInputJSON
		if err := json.Unmarshal(line, &raw); err != nil {
			parseFailed++
			continue
		}
		if raw.Ts.IsZero() {
			continue
		}
		records = append(records, HookRecord{
			Ts:    raw.Ts,
			Event: raw.Event,
			Agent: raw.Agent,
			Cwd:   raw.Cwd,
		})
	}
	if parseFailed > 0 {
		slog.Warn("skipped malformed legacy IDE hook input lines", "path", path, "count", parseFailed)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func loadHookRecordsFromDir(ctx context.Context, hookLogsDir string, skipNames ...string) ([]HookRecord, error) {
	entries, err := os.ReadDir(hookLogsDir)
	if err != nil {
		return nil, err
	}
	skip := make(map[string]struct{}, len(skipNames))
	for _, name := range skipNames {
		skip[name] = struct{}{}
	}

	var all []HookRecord
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, ok := skip[e.Name()]; ok {
			continue
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
