package monitor

import (
	"compress/gzip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testdataDir = "../../testdata/monitor"

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestLoadRecords(t *testing.T) {
	// session1.jsonl has records at 06:00, 07:00, 08:00
	recs, err := LoadRecords(testdataDir, mustTime("2026-04-20T06:30:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	// Expect records from session1 at 07:00 and 08:00, plus session2.jsonl.gz at 11:00,
	// plus incomplete.jsonl at 09:00 (only the complete line).
	// Filter: ts >= 06:30 → session1: 07:00, 08:00; incomplete: 09:00; gz: 11:00
	want := 4
	if len(recs) != want {
		t.Errorf("got %d records, want %d", len(recs), want)
		for _, r := range recs {
			t.Logf("  %s %s %s", r.Ts, r.Session, r.Event)
		}
	}
}

func TestLoadRecordsGzip(t *testing.T) {
	// session2.jsonl.gz has records at 05:00 and 11:00
	recs, err := LoadRecords(testdataDir, mustTime("2026-04-20T10:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	// Only the 11:00 record from gz passes the filter
	found := false
	for _, r := range recs {
		if r.Session == "sess-gz" && r.Event == "stop" {
			found = true
		}
	}
	if !found {
		t.Error("expected gz record with session=sess-gz event=stop")
	}
	// The 05:00 gz record should be filtered out
	for _, r := range recs {
		if r.Session == "sess-gz" && r.Event == "tool_use" {
			t.Error("gz record at 05:00 should have been filtered by since")
		}
	}
}

func TestLoadRecordsEmpty(t *testing.T) {
	recs, err := LoadRecords("/nonexistent/path/that/does/not/exist", time.Time{})
	if err != nil {
		t.Fatalf("expected nil error for missing dir, got: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("expected empty slice, got %d records", len(recs))
	}
}

func TestLoadRecordsIncompleteLine(t *testing.T) {
	// incomplete.jsonl has one complete line (09:00) and one truncated line
	recs, err := LoadRecords(testdataDir, mustTime("2026-04-20T08:30:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	// Find the record from incomplete.jsonl
	found := false
	for _, r := range recs {
		if r.Session == "sess-2" {
			found = true
			if r.Tool != "bash" {
				t.Errorf("expected tool=bash, got %q", r.Tool)
			}
		}
	}
	if !found {
		t.Error("expected record from incomplete.jsonl with session=sess-2")
	}
}

// writeJSONL creates a .jsonl file at path containing the given lines.
func writeJSONL(t testing.TB, path string, lines []string) {
	t.Helper()
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRecords_ReusesCacheWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeJSONL(t, filepath.Join(dir, "a.jsonl"), []string{
		`{"ts":"2026-04-20T09:00:00Z","session":"s1","event":"stop"}`,
	})
	writeJSONL(t, filepath.Join(dir, "b.jsonl"), []string{
		`{"ts":"2026-04-20T10:00:00Z","session":"s2","event":"stop"}`,
	})

	since := mustTime("2026-04-20T00:00:00Z")
	recs1, cache1, err := loadRecordsWithCache(dir, since, nil)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if len(recs1) != 2 {
		t.Fatalf("first load: got %d records, want 2", len(recs1))
	}
	if len(cache1) != 2 {
		t.Fatalf("first load: cache size = %d, want 2", len(cache1))
	}

	recs2, cache2, err := loadRecordsWithCache(dir, since, cache1)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if len(recs2) != 2 {
		t.Fatalf("second load: got %d records, want 2", len(recs2))
	}
	// Verify cache reuse: each entry in cache2 must point to the same backing
	// slice as cache1 (same data pointer), proving loadFile was not called.
	for path, e2 := range cache2 {
		e1, ok := cache1[path]
		if !ok {
			t.Fatalf("path %q missing from first cache", path)
		}
		if len(e1.recs) == 0 || len(e2.recs) == 0 {
			t.Fatalf("unexpected empty recs for %q", path)
		}
		if &e1.recs[0] != &e2.recs[0] {
			t.Errorf("path %q: recs slice re-allocated — cache not reused", path)
		}
	}
}

func TestLoadRecords_InvalidatesOnSizeChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.jsonl")
	writeJSONL(t, path, []string{
		`{"ts":"2026-04-20T09:00:00Z","session":"s1","event":"stop"}`,
	})

	since := mustTime("2026-04-20T00:00:00Z")
	recs1, cache1, err := loadRecordsWithCache(dir, since, nil)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if len(recs1) != 1 {
		t.Fatalf("first load: got %d, want 1", len(recs1))
	}

	// Append a second record. Force size change; also bump mtime to a known
	// future value to avoid sub-second resolution collisions on HFS+/NTFS.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"ts":"2026-04-20T10:00:00Z","session":"s2","event":"stop"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	recs2, cache2, err := loadRecordsWithCache(dir, since, cache1)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if len(recs2) != 2 {
		t.Fatalf("second load: got %d records, want 2", len(recs2))
	}
	if got := cache2[path].size; got == cache1[path].size {
		t.Errorf("cache size not updated: %d", got)
	}
}

func BenchmarkLoadRecordsRepeat(b *testing.B) {
	dir := b.TempDir()
	// Seed a handful of files so cache lookups dominate.
	for i := 0; i < 10; i++ {
		lines := make([]string, 50)
		for j := range lines {
			lines[j] = fmt.Sprintf(`{"ts":"2026-04-20T09:%02d:00Z","session":"s%d","event":"stop"}`, j%60, i)
		}
		writeJSONL(b, filepath.Join(dir, fmt.Sprintf("f%d.jsonl", i)), lines)
	}
	since := mustTime("2026-04-20T00:00:00Z")
	_, cache, err := loadRecordsWithCache(dir, since, nil)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, cache, err = loadRecordsWithCache(dir, since, cache)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestRecordCacheLoadReusesFiles(t *testing.T) {
	dir := t.TempDir()
	writeJSONL(t, filepath.Join(dir, "s1.jsonl"), []string{
		`{"ts":"2026-04-20T09:00:00Z","session":"s1","event":"stop"}`,
		`{"ts":"2026-04-20T10:00:00Z","session":"s1","event":"tool_use"}`,
	})

	c := NewRecordCache()
	recs1, err := c.Load(dir, time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	recs2, err := c.Load(dir, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs2) != len(recs1) {
		t.Errorf("got %d records on second load, want %d", len(recs2), len(recs1))
	}
}

func TestRecordCacheConcurrent(t *testing.T) {
	dir := t.TempDir()
	writeJSONL(t, filepath.Join(dir, "s1.jsonl"), []string{
		`{"ts":"2026-04-20T09:00:00Z","session":"s1","event":"stop"}`,
	})
	c := NewRecordCache()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = c.Load(dir, time.Time{}) }()
	}
	wg.Wait()
}

func TestRecordCacheConcurrentReads(t *testing.T) {
	dir := t.TempDir()
	for i, name := range []string{"a.jsonl", "b.jsonl", "c.jsonl"} {
		writeJSONL(t, filepath.Join(dir, name), []string{
			fmt.Sprintf(`{"ts":"2026-04-20T09:%02d:00Z","session":"s%d","event":"stop"}`, i, i),
		})
	}
	since := time.Time{}
	c := NewRecordCache()
	// Warm the cache.
	warm, err := c.Load(dir, since)
	if err != nil {
		t.Fatal(err)
	}
	want := len(warm)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				recs, err := c.Load(dir, since)
				if err != nil {
					t.Errorf("Load error: %v", err)
					return
				}
				if len(recs) != want {
					t.Errorf("got %d records, want %d", len(recs), want)
				}
			}
		}()
	}
	wg.Wait()
}

func BenchmarkRecordCacheLoadCold(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 10; i++ {
		lines := make([]string, 50)
		for j := range lines {
			lines[j] = fmt.Sprintf(`{"ts":"2026-04-20T09:%02d:00Z","session":"s%d","event":"stop"}`, j%60, i)
		}
		writeJSONL(b, filepath.Join(dir, fmt.Sprintf("f%d.jsonl", i)), lines)
	}
	since := mustTime("2026-04-20T00:00:00Z")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c := NewRecordCache()
		if _, err := c.Load(dir, since); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRecordCacheLoadWarm(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 10; i++ {
		lines := make([]string, 50)
		for j := range lines {
			lines[j] = fmt.Sprintf(`{"ts":"2026-04-20T09:%02d:00Z","session":"s%d","event":"stop"}`, j%60, i)
		}
		writeJSONL(b, filepath.Join(dir, fmt.Sprintf("f%d.jsonl", i)), lines)
	}
	since := mustTime("2026-04-20T00:00:00Z")
	c := NewRecordCache()
	if _, err := c.Load(dir, since); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Load(dir, since); err != nil {
			b.Fatal(err)
		}
	}
}

// writeGzipBombForTest writes a .jsonl.gz file to dir whose gzip stream
// decompresses to at least decompressedSize bytes of valid JSONL padding.
// Returns the full file path.
func writeGzipBombForTest(t *testing.T, dir string, decompressedSize int64) string {
	t.Helper()
	path := filepath.Join(dir, "bomb.jsonl.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	gw, err := gzip.NewWriterLevel(f, gzip.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	// One valid JSONL line with 4 KiB padding so the file compresses well.
	line := `{"ts":"2026-04-20T09:00:00Z","session":"bomb","event":"stop","prompt":"` + strings.Repeat("A", 4096) + `"}` + "\n"
	lineBytes := int64(len(line))
	var written int64
	for written < decompressedSize {
		if _, err := fmt.Fprint(gw, line); err != nil {
			t.Fatal(err)
		}
		written += lineBytes
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadRecords_CorruptGzipSkipped(t *testing.T) {
	dir := t.TempDir()
	// Write a valid .jsonl file alongside a corrupt .jsonl.gz file.
	writeJSONL(t, filepath.Join(dir, "valid.jsonl"), []string{
		`{"ts":"2026-04-20T09:00:00Z","session":"good","event":"stop"}`,
	})
	if err := os.WriteFile(filepath.Join(dir, "corrupt.jsonl.gz"), []byte("this is not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}

	recs, err := LoadRecords(dir, time.Time{})
	if err != nil {
		t.Fatalf("expected no error for corrupt gz, got: %v", err)
	}
	if len(recs) != 1 || recs[0].Session != "good" {
		t.Errorf("expected 1 valid record, got %d: %v", len(recs), recs)
	}
}

func TestLoadFile_GzipTooLarge(t *testing.T) {
	tmp := t.TempDir()
	writeGzipBombForTest(t, tmp, 300<<20) // 300 MiB decompressed

	recs, err := LoadRecords(tmp, time.Time{})
	// Expect either ErrLogTooLarge propagated or empty records (skip+warn strategy).
	if err != nil && !errors.Is(err, ErrLogTooLarge) {
		t.Fatalf("unexpected error: %v", err)
	}
	// The bomb file must not have been fully loaded (skip strategy: recs empty).
	for _, r := range recs {
		if r.Session == "bomb" {
			t.Errorf("bomb record should have been skipped, got session=%q", r.Session)
		}
	}
}
