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
)

// SessionCache caches parsed session files keyed by path, invalidated by mtime+size.
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
// cache may be nil (no caching). Returns a new cache for reuse.
// LoadSessions is not safe for concurrent calls with the same cache pointer.
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

	nextMeta := make(map[string]metaCacheEntry, len(entries))
	nextData := make(map[string]dataCacheEntry, len(entries))

	var sessions []ParsedSession
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue // skip .jsonl and .lock
		}

		jsonPath := filepath.Join(sessionsDir, name)
		info, err := e.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, nil, fmt.Errorf("stat %q: %w", jsonPath, err)
		}
		mtime, size := info.ModTime(), info.Size()

		var meta SessionMeta
		if prev, ok := oldMeta[jsonPath]; ok && prev.mtime.Equal(mtime) && prev.size == size {
			meta = prev.meta
			nextMeta[jsonPath] = prev
		} else {
			meta, err = parseSessionMetaFile(jsonPath)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				slog.Warn("skipped malformed session metadata", "path", jsonPath, "err", err)
				continue
			}
			nextMeta[jsonPath] = metaCacheEntry{mtime: mtime, size: size, meta: meta}
		}

		// Apply since filter using updated_at from metadata.
		if time.Time(meta.UpdatedAt).Before(since) {
			continue
		}

		// Apply cwd filter: skip sessions not rooted in cwdFilter.
		if cwdFilter != "" && !strings.HasPrefix(meta.Cwd, cwdFilter) {
			continue
		}

		// Parse corresponding .jsonl.
		uuid := strings.TrimSuffix(name, ".json")
		jsonlPath := filepath.Join(sessionsDir, uuid+".jsonl")

		jsonlInfo, err := os.Stat(jsonlPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // .jsonl missing — skip silently
			}
			return nil, nil, fmt.Errorf("stat %q: %w", jsonlPath, err)
		}
		jmtime, jsize := jsonlInfo.ModTime(), jsonlInfo.Size()

		var msgs []SessionMessage
		if prev, ok := oldData[jsonlPath]; ok && prev.mtime.Equal(jmtime) && prev.size == jsize {
			msgs = prev.msgs
			nextData[jsonlPath] = prev
		} else {
			msgs, err = parseSessionJSONLFile(jsonlPath)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				slog.Warn("skipped unreadable session jsonl", "path", jsonlPath, "err", err)
				continue
			}
			nextData[jsonlPath] = dataCacheEntry{mtime: jmtime, size: jsize, msgs: msgs}
		}

		sessions = append(sessions, ParsedSession{Meta: meta, Messages: msgs})
	}

	next := &SessionCache{meta: nextMeta, data: nextData}
	if sessions == nil {
		sessions = []ParsedSession{}
	}
	return sessions, next, nil
}
