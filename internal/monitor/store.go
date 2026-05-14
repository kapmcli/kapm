package monitor

import (
	"bufio"
	"cmp"
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
// since is passed to the SQL query to avoid loading ancient sessions on cold start.
// Caller still applies cwdFilter post-cache for finer filtering.
func (c *SQLiteCache) Load(ctx context.Context, dbPath string, since time.Time) ([]ParsedSession, error) {
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

	c.mu.Lock()
	defer c.mu.Unlock()
	if info.ModTime().Equal(c.mtime) {
		return c.sessions, nil
	}
	sessions, err := LoadSessionsSQLite(ctx, dbPath, since, "")
	if err != nil {
		return nil, err
	}
	c.mtime = info.ModTime()
	c.sessions = sessions
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
	t0 := time.Now()

	// Load v2 sessions, v1 sqlite, and hook logs in parallel.
	var (
		v2Sessions []ParsedSession
		nextCache  *SessionCache
		v2Err      error

		allV1 []ParsedSession
		v1Err error

		hookLogs []HookRecord
		hookErr  error
	)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		v2Sessions, nextCache, v2Err = LoadSessions(ctx, sessionsDir, since, cwdFilter, cache)
	}()

	if sqliteDBPath != "" && sqliteCache != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allV1, v1Err = sqliteCache.Load(ctx, sqliteDBPath, since)
		}()
	}

	if hookLogsDir != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hookLogs, hookErr = loadAllHookRecords(ctx, hookLogsDir)
			if hookErr != nil && errors.Is(hookErr, fs.ErrNotExist) {
				hookErr = nil
			}
		}()
	}

	wg.Wait()

	if v2Err != nil {
		return nil, nil, fmt.Errorf("load sessions: %w", v2Err)
	}
	if v1Err != nil {
		return nil, nil, fmt.Errorf("load sqlite sessions: %w", v1Err)
	}
	if hookErr != nil {
		return nil, nil, fmt.Errorf("load hook logs: %w", hookErr)
	}
	slog.Debug("LoadAll: parallel load done", "v2", len(v2Sessions), "v1", len(allV1), "hooks", len(hookLogs), "elapsed", time.Since(t0))

	var allSessions []ParsedSession
	allSessions = append(allSessions, v2Sessions...)

	if len(allV1) > 0 {
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
		v2IDs := make(map[string]struct{}, len(v2Sessions))
		for _, s := range v2Sessions {
			v2IDs[s.Meta.SessionID] = struct{}{}
		}
		for _, s := range v1Filtered {
			if _, dup := v2IDs[s.Meta.SessionID]; !dup {
				allSessions = append(allSessions, s)
			}
		}
		slog.Debug("LoadAll: v1 sqlite", "total", len(allV1), "filtered", len(v1Filtered))
	}

	t3 := time.Now()
	records := MergeSessions(allSessions, hookLogs)
	if records == nil {
		records = []MergedRecord{}
	}
	slog.Debug("LoadAll: merge", "sessions", len(allSessions), "records", len(records), "elapsed", time.Since(t3))

	if ideBaseDir != "" {
		t4 := time.Now()
		ideSessions, ideErr := LoadIDESessions(ctx, ideBaseDir, since, cwdFilter)
		if ideErr != nil {
			slog.Warn("load ide sessions", "err", ideErr)
		} else if len(ideSessions) > 0 {
			slog.Debug("LoadAll: ide sessions", "count", len(ideSessions), "elapsed", time.Since(t4))

			t5 := time.Now()
			execIDs := collectExecutionIDs(ideSessions)
			execResults, execErr := LoadIDEExecutions(ctx, ideBaseDir, execIDs)
			if execErr != nil {
				slog.Warn("load ide executions", "err", execErr)
			}
			slog.Debug("LoadAll: ide executions", "ids", len(execIDs), "elapsed", time.Since(t5))

			t6 := time.Now()
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
			slog.Debug("LoadAll: ide merge+hooks", "ideRecords", len(ideRecords), "elapsed", time.Since(t6))
		}
	}

	slog.Debug("LoadAll: total", "records", len(records), "elapsed", time.Since(t0))
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

type hookDedup struct {
	ts                          time.Time
	session, event, agent, tool string
	cwd, shellExit              string
}

func dedupeHookRecords(records []HookRecord) []HookRecord {
	if len(records) < 2 {
		return records
	}
	seen := make(map[hookDedup]struct{}, len(records))
	out := records[:0]
	for _, rec := range records {
		key := hookDedup{rec.Ts, rec.Session, rec.Event, rec.Agent, rec.Tool, rec.Cwd, rec.ShellExitStatus}
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
			return cmp.Compare(a.Session, b.Session)
		}
		if c := a.Ts.Compare(b.Ts); c != 0 {
			return c
		}
		if a.Event != b.Event {
			return cmp.Compare(a.Event, b.Event)
		}
		if a.Tool != b.Tool {
			return cmp.Compare(a.Tool, b.Tool)
		}
		return cmp.Compare(a.Agent, b.Agent)
	})
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
