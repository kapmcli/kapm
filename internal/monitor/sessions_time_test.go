package monitor

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRFC3339Time_Unmarshal(t *testing.T) {
	t.Run("RFC3339 value", func(t *testing.T) {
		var meta SessionMeta
		err := json.Unmarshal([]byte(`{"created_at":"2026-04-27T10:00:00Z","updated_at":"2026-04-27T11:00:00Z","session_id":"s1"}`), &meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
		if !time.Time(meta.CreatedAt).Equal(want) {
			t.Errorf("CreatedAt: got %v, want %v", meta.CreatedAt, want)
		}
		wantU := time.Date(2026, 4, 27, 11, 0, 0, 0, time.UTC)
		if !time.Time(meta.UpdatedAt).Equal(wantU) {
			t.Errorf("UpdatedAt: got %v, want %v", meta.UpdatedAt, wantU)
		}
	})

	t.Run("empty string → zero time, no error", func(t *testing.T) {
		var meta SessionMeta
		err := json.Unmarshal([]byte(`{"created_at":"","updated_at":"","session_id":"s1"}`), &meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !time.Time(meta.CreatedAt).IsZero() {
			t.Errorf("CreatedAt: expected zero, got %v", meta.CreatedAt)
		}
		if !time.Time(meta.UpdatedAt).IsZero() {
			t.Errorf("UpdatedAt: expected zero, got %v", meta.UpdatedAt)
		}
	})

	t.Run("missing field → zero time", func(t *testing.T) {
		var meta SessionMeta
		err := json.Unmarshal([]byte(`{"session_id":"s1"}`), &meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !time.Time(meta.CreatedAt).IsZero() {
			t.Errorf("CreatedAt: expected zero, got %v", meta.CreatedAt)
		}
	})

	t.Run("malformed non-empty → error", func(t *testing.T) {
		var meta SessionMeta
		err := json.Unmarshal([]byte(`{"created_at":"not-a-date","session_id":"s1"}`), &meta)
		if err == nil {
			t.Fatal("expected error for malformed timestamp, got nil")
		}
	})
}
