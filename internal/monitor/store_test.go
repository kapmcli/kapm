package monitor

import (
	"context"
	"testing"
	"time"
)

func TestLoadAll_EmptyDirs(t *testing.T) {
	t.Parallel()
	recs, cache, err := LoadAll(context.Background(), "/nonexistent/sessions", "/nonexistent/hooks", time.Time{}, "", nil)
	if err != nil {
		t.Fatalf("expected nil error for missing dirs, got: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("expected empty slice, got %d records", len(recs))
	}
	if cache == nil {
		t.Error("expected non-nil cache")
	}
}

func TestLoadAll_CancelledCtx(t *testing.T) {
	t.Parallel()
	// With an empty dir and cancelled ctx, LoadSessions returns empty without error
	// because the loop body never executes. This is acceptable behavior.
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recs, _, err := LoadAll(ctx, dir, "", time.Time{}, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("expected empty records, got %d", len(recs))
	}
}
