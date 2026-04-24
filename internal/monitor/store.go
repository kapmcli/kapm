package monitor

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrLogTooLarge indicates a gzipped log file decompresses beyond
// maxDecompressedLogBytes and was skipped.
var ErrLogTooLarge = errors.New("decompressed log exceeded limit")

// maxDecompressedLogBytes caps gzip decompression at 256 MiB.
const maxDecompressedLogBytes = 256 << 20

// Record is an exported version of the JSONL log record written by internal/hook/handler.go.
type Record struct {
	Ts                time.Time       `json:"ts"`
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

// fileEntry is a cached parse result for a log file, keyed by (mtime, size).
// Invalidation: any change in mtime OR size triggers re-read (HFS+/NTFS have
// second-grade mtime resolution, so size alone is a required tie-breaker).
type fileEntry struct {
	mtime time.Time
	size  int64
	recs  []Record
}

// LoadRecords reads all .jsonl and .jsonl.gz files in logsDir and returns records
// with Ts >= since. Returns an empty slice (not an error) if the directory is missing.
func LoadRecords(logsDir string, since time.Time) ([]Record, error) {
	recs, _, err := loadRecordsWithCache(logsDir, since, nil)
	return recs, err
}

// RecordCache caches parsed log file contents keyed by path, invalidated by
// file mtime+size. Safe for concurrent use.
type RecordCache struct {
	mu sync.Mutex
	m  map[string]fileEntry
}

// NewRecordCache returns an empty cache.
func NewRecordCache() *RecordCache {
	return &RecordCache{m: map[string]fileEntry{}}
}

// Load reads all .jsonl/.jsonl.gz files in logsDir returning records with
// Ts >= since. Files unchanged since the last call are served from cache.
// Safe for concurrent use.
func (c *RecordCache) Load(logsDir string, since time.Time) ([]Record, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	recs, next, err := loadRecordsWithCache(logsDir, since, c.m)
	if err != nil {
		return nil, err
	}
	c.m = next
	return recs, nil
}

// loadRecordsWithCache is the cache-aware implementation behind LoadRecords.
// It reuses entries from cache when (mtime, size) are unchanged, and returns a
// fresh cache map containing only files that still exist. Only successful
// file reads are inserted into the returned cache.
func loadRecordsWithCache(logsDir string, since time.Time, cache map[string]fileEntry) ([]Record, map[string]fileEntry, error) {
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Record{}, map[string]fileEntry{}, nil
		}
		return nil, nil, err
	}

	next := make(map[string]fileEntry, len(entries))
	var records []Record
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		var isGz bool
		switch {
		case strings.HasSuffix(name, ".jsonl.gz"):
			isGz = true
		case strings.HasSuffix(name, ".jsonl"):
			isGz = false
		default:
			continue
		}

		path := filepath.Join(logsDir, name)
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // rotated away between ReadDir and Stat
			}
			return nil, nil, err
		}
		mtime := info.ModTime()
		size := info.Size()

		if prev, ok := cache[path]; ok && prev.mtime.Equal(mtime) && prev.size == size {
			records = append(records, prev.recs...)
			next[path] = prev
			continue
		}

		recs, err := loadFile(path, isGz)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // rotated away during read
			}
			if errors.Is(err, ErrLogTooLarge) {
				slog.Warn("decompressed log exceeded limit", "file", path)
				continue
			}
			// NEW: skip corrupt gzip files instead of aborting the entire poll
			if isGz {
				slog.Warn("skipping corrupt gzip log", "file", path, "err", err)
				continue
			}
			return nil, nil, err
		}
		records = append(records, recs...)
		next[path] = fileEntry{mtime: mtime, size: size, recs: recs}
	}

	// Apply since filter after all records are collected (not at parse time),
	// so cached entries remain valid regardless of when they were populated.
	filtered := records[:0]
	for _, r := range records {
		if !r.Ts.Before(since) {
			filtered = append(filtered, r)
		}
	}
	if filtered == nil {
		filtered = []Record{}
	}
	return filtered, next, nil
}

func loadFile(path string, isGz bool) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	// Read-only close: ignoring Close error is safe; no data integrity risk on read paths.
	defer func() { _ = f.Close() }()

	if !isGz {
		return parseRecords(f)
	}

	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	// Read-only close: ignoring Close error is safe; no data integrity risk on read paths.
	defer func() { _ = gr.Close() }()

	limited := io.LimitReader(gr, maxDecompressedLogBytes)
	recs, err := parseRecords(limited)
	if err != nil {
		return nil, err
	}
	// +1 probe: if the limit was hit, gr still has unread bytes.
	var probe [1]byte
	if n, _ := gr.Read(probe[:]); n > 0 {
		return nil, ErrLogTooLarge
	}
	return recs, nil
}

func parseRecords(r io.Reader) ([]Record, error) {
	var records []Record
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024) // 10MB max line
	var skipped int
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec Record
		if err := json.Unmarshal(line, &rec); err != nil {
			skipped++
			continue // skip incomplete/malformed lines
		}
		records = append(records, rec)
	}
	if skipped > 0 {
		slog.Warn("skipped malformed log records", "count", skipped)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}
