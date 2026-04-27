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
func AggregateDetailFnForTest(fn func(context.Context, []monitor.Record, time.Time) (monitor.DetailedMetrics, error)) func() {
	orig := aggregateDetailFn
	aggregateDetailFn = fn
	return func() { aggregateDetailFn = orig }
}

// DashboardSessionsFromCache returns the cached dashboardSessions slice for
// the current cache entry, or nil if the cache is empty.
func (s *Server) DashboardSessionsFromCache() []monitor.SessionMetric {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	if s.metricsCache == nil {
		return nil
	}
	return s.metricsCache.dashboardSessions
}

// SSEMaxForTest returns the configured SSE cap for white-box tests.
func (s *Server) SSEMaxForTest() int32 { return s.sseMax }
