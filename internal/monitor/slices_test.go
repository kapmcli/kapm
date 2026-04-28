package monitor

import (
	"slices"
	"testing"
	"time"
)

func TestSortToolCallByTsDesc(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 1, 11, 0, 0, 0, time.UTC)
	calls := []ToolCall{
		{Ts: t1, Session: "s2", Agent: "a", Tool: "t", InputSummary: "i"},
		{Ts: t2, Session: "s1", Agent: "a", Tool: "t", InputSummary: "i"},
		{Ts: t1, Session: "s1", Agent: "a", Tool: "t", InputSummary: "i"},
	}
	slices.SortFunc(calls, sortToolCallByTsDesc)
	// t2 first (desc), then t1/s1 before t1/s2
	if calls[0].Ts != t2 {
		t.Errorf("expected t2 first, got %v", calls[0].Ts)
	}
	if calls[1].Session != "s1" || calls[2].Session != "s2" {
		t.Errorf("expected s1 before s2 for same ts, got %v %v", calls[1].Session, calls[2].Session)
	}
}

func TestSortToolMetricByCallCountDescNameAsc(t *testing.T) {
	metrics := []ToolMetric{
		{Name: "b", CallCount: 5},
		{Name: "a", CallCount: 5},
		{Name: "c", CallCount: 10},
	}
	slices.SortFunc(metrics, sortToolMetricByCallCountDescNameAsc)
	if metrics[0].Name != "c" {
		t.Errorf("expected c first (highest count), got %v", metrics[0].Name)
	}
	if metrics[1].Name != "a" || metrics[2].Name != "b" {
		t.Errorf("expected a before b for same count, got %v %v", metrics[1].Name, metrics[2].Name)
	}
}

func TestSortToolDetailByCallCountDescNameAsc(t *testing.T) {
	details := []ToolDetail{
		{ToolMetric: ToolMetric{Name: "b", CallCount: 3}},
		{ToolMetric: ToolMetric{Name: "a", CallCount: 3}},
		{ToolMetric: ToolMetric{Name: "c", CallCount: 7}},
	}
	slices.SortFunc(details, sortToolDetailByCallCountDescNameAsc)
	if details[0].Name != "c" {
		t.Errorf("expected c first, got %v", details[0].Name)
	}
	if details[1].Name != "a" || details[2].Name != "b" {
		t.Errorf("expected a before b for same count, got %v %v", details[1].Name, details[2].Name)
	}
}

func TestSortTimeAsc(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 9, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2024, 1, 1, 11, 0, 0, 0, time.UTC)
	times := []time.Time{t3, t1, t2}
	slices.SortFunc(times, sortTimeAsc)
	if times[0] != t1 || times[1] != t2 || times[2] != t3 {
		t.Errorf("expected ascending order, got %v", times)
	}
}
