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

	"github.com/kapmcli/kapm/internal/fileutil"
	"golang.org/x/sync/errgroup"
)

// SessionCache caches parsed session files keyed by path, invalidated by
// mtime+size. It is populated by LoadSessions, which returns a new
// *SessionCache; callers pass the returned cache back on the next call to
// reuse parse results.
//
// Concurrency: a single *SessionCache must not be shared by concurrent
// LoadSessions calls. The serve package synchronizes calls through
// golang.org/x/sync/singleflight (see internal/serve/server.go loadMetrics).
// The internal mu field guards the short window where LoadSessions
// snapshots the previous cache into local variables, but callers must not
// rely on it for higher-level coordination.
type SessionCache struct {
	mu   sync.Mutex
	meta map[string]metaCacheEntry
	data map[string]dataCacheEntry
}

// NewSessionCache returns an initialized *SessionCache.
func NewSessionCache() *SessionCache {
	return &SessionCache{
		meta: map[string]metaCacheEntry{},
		data: map[string]dataCacheEntry{},
	}
}

type metaCacheEntry struct {
	mtime time.Time
	size  int64
	meta  SessionMeta
}

type dataCacheEntry struct {
	mtime time.Time
	size  int64
	msgs  []SessionMessage
}

// sessionJSONLMaxBytes caps the size of a single session .jsonl file that
// kapm will parse. Files beyond this size are skipped with a Warn log to
// avoid OOM on pathologically large logs. 100 MiB.
const mebibyte = 1 << 20
const sessionJSONLMaxBytes = 100 * mebibyte

const sessionLoadParallelism = 8

// ParsedSession holds the metadata and messages for one session.
type ParsedSession struct {
	Meta     SessionMeta
	Messages []SessionMessage
}

func parseSessionMetaFile(path string) (SessionMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionMeta{}, err
	}
	defer func() { _ = f.Close() }()
	return ParseSessionMeta(f)
}

func parseSessionJSONLFile(path string) ([]SessionMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	msgs, _, err := ParseSessionJSONL(f)
	return msgs, err
}

// LoadSessions scans sessionsDir for .json metadata files, filters by
// updated_at >= since and optionally by cwd prefix, and parses the
// corresponding .jsonl for matching sessions. cwdFilter limits to sessions
// whose cwd starts with the given path; empty means no filter.
// cache may be nil (no caching). Returns a fresh *SessionCache that callers
// should pass on the next invocation. The caller is responsible for
// serializing LoadSessions calls that share the same cache pointer;
// see SessionCache documentation.
func LoadSessions(ctx context.Context, sessionsDir string, since time.Time, cwdFilter string, cache *SessionCache) ([]ParsedSession, *SessionCache, error) {
	isLink, err := fileutil.IsSymlinkPath(sessionsDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("sessions dir lstat %q: %w", sessionsDir, err)
	}
	if isLink {
		return nil, nil, fmt.Errorf("refusing to read symlink sessions dir: %s", sessionsDir)
	}

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []ParsedSession{}, NewSessionCache(), nil
		}
		return nil, nil, fmt.Errorf("sessions dir read %q: %w", sessionsDir, err)
	}

	var oldMeta map[string]metaCacheEntry
	var oldData map[string]dataCacheEntry
	if cache != nil {
		cache.mu.Lock()
		oldMeta = cache.meta
		oldData = cache.data
		cache.mu.Unlock()
	}
	if oldMeta == nil {
		oldMeta = map[string]metaCacheEntry{}
	}
	if oldData == nil {
		oldData = map[string]dataCacheEntry{}
	}

	sessionEntries := make([]fs.DirEntry, 0, len(entries))
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		sessionEntries = append(sessionEntries, e)
	}

	results := make([]sessionLoadResult, len(sessionEntries))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(sessionLoadParallelism)
	for i, e := range sessionEntries {
		if err := gctx.Err(); err != nil {
			return nil, nil, err
		}
		g.Go(func() error {
			result, err := loadSessionEntry(gctx, sessionsDir, since, cwdFilter, oldMeta, oldData, e)
			if err != nil {
				return err
			}
			results[i] = result
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, nil, err
	}

	nextMeta := make(map[string]metaCacheEntry, len(sessionEntries))
	nextData := make(map[string]dataCacheEntry, len(sessionEntries))
	sessions := make([]ParsedSession, 0, len(sessionEntries))
	for _, result := range results {
		if result.hasMeta {
			nextMeta[result.jsonPath] = result.metaEntry
		}
		if result.hasData {
			nextData[result.jsonlPath] = result.dataEntry
		}
		if result.hasSession {
			sessions = append(sessions, result.session)
		}
	}

	next := &SessionCache{meta: nextMeta, data: nextData}
	if sessions == nil {
		sessions = []ParsedSession{}
	}
	return sessions, next, nil
}

type sessionLoadResult struct {
	jsonPath   string
	jsonlPath  string
	metaEntry  metaCacheEntry
	dataEntry  dataCacheEntry
	session    ParsedSession
	hasMeta    bool
	hasData    bool
	hasSession bool
}

func loadSessionEntry(ctx context.Context, sessionsDir string, since time.Time, cwdFilter string, oldMeta map[string]metaCacheEntry, oldData map[string]dataCacheEntry, e fs.DirEntry) (sessionLoadResult, error) {
	if err := ctx.Err(); err != nil {
		return sessionLoadResult{}, err
	}
	if e.IsDir() {
		return sessionLoadResult{}, nil
	}
	name := e.Name()
	if !strings.HasSuffix(name, ".json") {
		return sessionLoadResult{}, nil // skip .jsonl and .lock
	}

	jsonPath := filepath.Join(sessionsDir, name)
	info, err := e.Info()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return sessionLoadResult{}, nil
		}
		return sessionLoadResult{}, fmt.Errorf("stat %q: %w", jsonPath, err)
	}
	mtime, size := info.ModTime(), info.Size()

	result := sessionLoadResult{jsonPath: jsonPath}
	var meta SessionMeta
	if prev, ok := oldMeta[jsonPath]; ok && prev.mtime.Equal(mtime) && prev.size == size {
		meta = prev.meta
		result.metaEntry = prev
		result.hasMeta = true
	} else {
		meta, err = parseSessionMetaFile(jsonPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return sessionLoadResult{}, nil
			}
			slog.Warn("skipped malformed session metadata", "path", jsonPath, "err", err)
			return sessionLoadResult{}, nil
		}
		result.metaEntry = metaCacheEntry{mtime: mtime, size: size, meta: meta}
		result.hasMeta = true
	}

	// Apply since filter using updated_at from metadata.
	if time.Time(meta.UpdatedAt).Before(since) {
		return result, nil
	}

	// Apply cwd filter: skip sessions not rooted in cwdFilter.
	if cwdFilter != "" && !strings.HasPrefix(meta.Cwd, cwdFilter) {
		return result, nil
	}

	// Parse corresponding .jsonl.
	uuid := strings.TrimSuffix(name, ".json")
	jsonlPath := filepath.Join(sessionsDir, uuid+".jsonl")
	result.jsonlPath = jsonlPath

	jsonlInfo, err := os.Stat(jsonlPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return result, nil // .jsonl missing — skip silently
		}
		return sessionLoadResult{}, fmt.Errorf("stat %q: %w", jsonlPath, err)
	}
	jmtime, jsize := jsonlInfo.ModTime(), jsonlInfo.Size()

	if jsize > sessionJSONLMaxBytes {
		slog.Warn("session jsonl too large; skipping",
			"path", jsonlPath,
			"size_bytes", jsize,
			"max_bytes", sessionJSONLMaxBytes)
		result.session = ParsedSession{Meta: meta}
		result.hasSession = true
		return result, nil
	}

	var msgs []SessionMessage
	if prev, ok := oldData[jsonlPath]; ok && prev.mtime.Equal(jmtime) && prev.size == jsize {
		msgs = prev.msgs
		result.dataEntry = prev
		result.hasData = true
	} else {
		msgs, err = parseSessionJSONLFile(jsonlPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return result, nil
			}
			slog.Warn("skipped unreadable session jsonl", "path", jsonlPath, "err", err)
			return result, nil
		}
		result.dataEntry = dataCacheEntry{mtime: jmtime, size: jsize, msgs: msgs}
		result.hasData = true
	}

	result.session = ParsedSession{Meta: meta, Messages: msgs}
	result.hasSession = true
	return result, nil
}
