package serve

import (
	"context"
	"time"

	"github.com/kapmcli/kapm/internal/monitor"
)

// ComputeDashboardSessions exposes computeDashboardSessions for white-box tests.
var ComputeDashboardSessions = computeDashboardSessions

// AggregateDetailFnForTest overrides aggregateDetailFn for tests and restores
// it via the returned cleanup function.
func AggregateDetailFnForTest(fn func(context.Context, []monitor.MergedRecord, time.Time) (monitor.DetailedMetrics, error)) func() {
	orig := aggregateDetailFn
	aggregateDetailFn = fn
	return func() { aggregateDetailFn = orig }
}

// DashboardSessionsFromCache returns the cached dashboardSessions slice for
// the current cache entry, or nil if the cache is empty.
func (s *Server) DashboardSessionsFromCache() []monitor.SessionMetric {
	s.metricsMu.RLock()
	defer s.metricsMu.RUnlock()
	if s.metricsCache == nil {
		return nil
	}
	return s.metricsCache.dashboardSessions
}

// SSEMaxForTest returns the configured SSE cap for white-box tests.
func (s *Server) SSEMaxForTest() int32 { return s.sseMax }

// OpenBrowserFnForTest replaces the platform opener with fn and returns a
// cleanup function that restores the original.
func OpenBrowserFnForTest(fn func(string) error) func() {
	orig := openBrowserFn
	openBrowserFn = fn
	return func() { openBrowserFn = orig }
}
