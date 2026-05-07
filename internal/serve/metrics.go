package serve

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/kapmcli/kapm/internal/kirocliusage"
	"github.com/kapmcli/kapm/internal/monitor"
)

// loadMetrics reads and aggregates records for the configured window.
// Results are cached for s.ttl to avoid repeated AggregateDetail calls.
// Only one goroutine runs AggregateDetail per TTL window (singleflight).
// ctx is forwarded to the record load and aggregation so request cancellation
// aborts in-flight work.
func (s *Server) loadMetrics(ctx context.Context) (loadedMetrics, error) {
	now := s.now()

	if lm, ok := s.cachedMetrics(now); ok {
		return lm, nil
	}

	v, err, _ := s.metricsSF.Do("metrics", func() (any, error) {
		// Re-check: another goroutine may have populated the cache while we waited.
		if lm, ok := s.cachedMetrics(now); ok {
			return lm, nil
		}

		lm, err := s.loadFreshMetrics(ctx, now)
		if err != nil {
			return loadedMetrics{}, err
		}
		s.storeMetrics(now, lm)
		return lm, nil
	})
	if err != nil {
		return loadedMetrics{}, err
	}
	lm, ok := v.(loadedMetrics)
	if !ok {
		return loadedMetrics{}, fmt.Errorf("unexpected metrics type %T", v)
	}
	return lm, nil
}

func (s *Server) cachedMetrics(now time.Time) (loadedMetrics, bool) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()

	if s.metricsCache == nil || now.Sub(s.metricsCache.storedAt) >= s.ttl {
		return loadedMetrics{}, false
	}
	entry := s.metricsCache
	return loadedMetrics{dm: entry.dm, sessions: entry.dashboardSessions}, true
}

func (s *Server) loadFreshMetrics(ctx context.Context, now time.Time) (loadedMetrics, error) {
	recs, nextCache, err := monitor.LoadAll(
		ctx,
		s.opts.SessionsDir,
		s.opts.LogsDir,
		s.opts.IDEBaseDir,
		s.opts.SQLiteDBPath,
		now.Add(-s.opts.Since),
		s.opts.CwdFilter,
		s.cache,
		s.sqliteCache,
	)
	if err != nil {
		return loadedMetrics{}, fmt.Errorf("serve load records: %w", err)
	}
	s.cache = nextCache

	dm, err := aggregateDetailFn(ctx, recs, now)
	if err != nil {
		return loadedMetrics{}, fmt.Errorf("serve aggregate records: %w", err)
	}
	sessions := computeDashboardSessions(dm.Overview.Sessions)
	return loadedMetrics{dm: dm, sessions: sessions}, nil
}

func (s *Server) storeMetrics(now time.Time, lm loadedMetrics) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()

	s.metricsCache = &metricsCacheEntry{
		dm:                lm.dm,
		dashboardSessions: lm.sessions,
		storedAt:          now,
	}
}

func (s *Server) currentKiroUsage(now time.Time) (*kirocliusage.Usage, bool) {
	if s.kiroUsageRead == nil {
		return nil, false
	}
	usage, fresh, checked := s.cachedKiroUsage(now)
	if !fresh {
		s.refreshKiroUsageAsync(now)
	}
	return usage, checked
}

func (s *Server) refreshKiroUsageAsync(now time.Time) {
	if s.kiroUsageRead == nil {
		return
	}
	ctx := s.rootCtx
	if ctx == nil {
		slog.Warn("serve refreshKiroUsageAsync: rootCtx is nil, falling back to context.Background()")
		ctx = context.Background()
	}
	s.kiroUsageSF.DoChan("kiro-usage", func() (any, error) {
		return s.readAndStoreKiroUsage(ctx, now), nil
	})
}

func (s *Server) refreshKiroUsage(ctx context.Context, now time.Time) *kirocliusage.Usage {
	if s.kiroUsageRead == nil {
		return nil
	}
	v, err, _ := s.kiroUsageSF.Do("kiro-usage", func() (any, error) {
		return s.readAndStoreKiroUsage(ctx, now), nil
	})
	if err != nil {
		return nil
	}
	usage, _ := v.(*kirocliusage.Usage)
	return usage
}

func (s *Server) readAndStoreKiroUsage(ctx context.Context, now time.Time) *kirocliusage.Usage {
	usage, ok, err := s.kiroUsageRead(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("serve load kiro usage", "err", err)
		}
		s.storeKiroUsageIfEmpty(now, nil)
		return nil
	}
	if !ok {
		s.storeKiroUsageIfEmpty(now, nil)
		return nil
	}
	s.storeKiroUsage(now, &usage)
	return &usage
}

func (s *Server) cachedKiroUsage(now time.Time) (*kirocliusage.Usage, bool, bool) {
	s.kiroUsageMu.Lock()
	defer s.kiroUsageMu.Unlock()

	if s.kiroUsageCache == nil {
		return nil, false, false
	}
	return s.kiroUsageCache.usage, now.Sub(s.kiroUsageCache.storedAt) < s.kiroUsageTTL, true
}

func (s *Server) storeKiroUsage(now time.Time, usage *kirocliusage.Usage) {
	s.kiroUsageMu.Lock()
	defer s.kiroUsageMu.Unlock()
	s.kiroUsageCache = &kiroUsageCacheEntry{usage: usage, storedAt: now}
}

func (s *Server) storeKiroUsageIfEmpty(now time.Time, usage *kirocliusage.Usage) {
	s.kiroUsageMu.Lock()
	defer s.kiroUsageMu.Unlock()
	if s.kiroUsageCache != nil && s.kiroUsageCache.usage != nil {
		s.kiroUsageCache.storedAt = now
		return
	}
	s.kiroUsageCache = &kiroUsageCacheEntry{usage: usage, storedAt: now}
}

// computeDashboardSessions groups sessions by ID for the dashboard's Recent
// Sessions panel. Within each ID group rows are ordered by LastActivity desc;
// groups themselves are ordered by their most-recent LastActivity desc so the
// panel stays newest-first. The input slice is not modified.
func computeDashboardSessions(sessions []monitor.SessionMetric) []monitor.SessionMetric {
	out := slices.Clone(sessions)
	groupLast := map[string]time.Time{}
	for _, s := range out {
		if s.LastActivity.After(groupLast[s.ID]) {
			groupLast[s.ID] = s.LastActivity
		}
	}
	slices.SortStableFunc(out, func(a, b monitor.SessionMetric) int {
		if a.ID != b.ID {
			if c := groupLast[b.ID].Compare(groupLast[a.ID]); c != 0 {
				return c
			}
			return strings.Compare(a.ID, b.ID)
		}
		return b.LastActivity.Compare(a.LastActivity)
	})
	return out
}

// buildFilteredMetrics constructs DetailedMetrics from pre-filtered agents and sessions,
// re-aggregating tools from session timelines.
func buildFilteredMetrics(agents []monitor.AgentDetail, sessions []monitor.SessionDetail) monitor.DetailedMetrics {
	var out monitor.DetailedMetrics
	for _, a := range agents {
		out.Agents = append(out.Agents, a)
		out.Overview.Agents = append(out.Overview.Agents, a.AgentMetric)
	}
	for _, sd := range sessions {
		out.Sessions = append(out.Sessions, sd)
		out.Overview.Sessions = append(out.Overview.Sessions, sd.SessionMetric)
	}
	out.Tools, out.Overview.Tools = monitor.AggregateToolsFromTimeline(sessions)
	return out
}

// filterBySession narrows dm to a single session id and returns a merged
// SessionDetail across all agents participating in that sid (see
// monitor.MergeSessionDetails). Tools and Overview.Tools are re-aggregated
// from the pre-merge per-agent timelines via
// monitor.AggregateToolsFromTimeline so tool attribution stays per-agent in
// each ToolCall. Skills are not re-aggregated (session attribution for
// skills is not preserved in DetailedMetrics) and remain empty.
func filterBySession(dm monitor.DetailedMetrics, id string) monitor.DetailedMetrics {
	out := monitor.DetailedMetrics{}
	matches := sessionDetailsByID(dm.Sessions, id)
	if len(matches) == 0 {
		return out
	}
	merged, _ := monitor.MergeSessionDetails(matches)
	out.Sessions = []monitor.SessionDetail{merged}
	out.Overview.Sessions = []monitor.SessionMetric{merged.SessionMetric}
	out.Tools, out.Overview.Tools = monitor.AggregateToolsFromTimeline(matches)
	return out
}

// filterByAgent narrows dm to a single agent name. Tools and Overview.Tools
// are re-aggregated from the agent's session timelines via
// monitor.AggregateToolsFromTimeline. Skills are not re-aggregated (session
// attribution for skills is not preserved in DetailedMetrics) and remain
// empty in the result.
func filterByAgent(dm monitor.DetailedMetrics, name string) monitor.DetailedMetrics {
	var agents []monitor.AgentDetail
	for _, a := range dm.Agents {
		if a.Name == name {
			agents = append(agents, a)
		}
	}
	var sessions []monitor.SessionDetail
	for _, sd := range dm.Sessions {
		if sd.Agent == name {
			sessions = append(sessions, sd)
		}
	}
	return buildFilteredMetrics(agents, sessions)
}

func sessionDetailsByID(sessions []monitor.SessionDetail, id string) []monitor.SessionDetail {
	var matches []monitor.SessionDetail
	for _, sd := range sessions {
		if sd.ID == id {
			matches = append(matches, sd)
		}
	}
	return matches
}

func sessionDetailByIDAndAgent(sessions []monitor.SessionDetail, id, agent string) (monitor.SessionDetail, []monitor.AgentRef, bool) {
	var target monitor.SessionDetail
	var others []monitor.AgentRef
	var found bool
	for _, sd := range sessions {
		if sd.ID != id {
			continue
		}
		if sd.Agent == agent {
			target = sd
			found = true
			continue
		}
		others = append(others, monitor.AgentRef{Agent: sd.Agent, AgentKey: sd.AgentKey})
	}
	return target, others, found
}

func agentDetailByName(agents []monitor.AgentDetail, name string) (monitor.AgentDetail, bool) {
	for _, agent := range agents {
		if agent.Name == name {
			return agent, true
		}
	}
	return monitor.AgentDetail{}, false
}

func toolDetailByName(tools []monitor.ToolDetail, name string) (monitor.ToolDetail, bool) {
	for _, tool := range tools {
		if tool.Name == name {
			return tool, true
		}
	}
	canonical := monitor.CanonicalToolNameForAggregation(name)
	if canonical != name {
		for _, tool := range tools {
			if tool.Name == canonical {
				return tool, true
			}
		}
	}
	return monitor.ToolDetail{}, false
}

// distinctSessionIDs returns session IDs in order of first appearance.
func distinctSessionIDs(sessions []monitor.SessionMetric) []string {
	seen := map[string]struct{}{}
	var ids []string
	for _, s := range sessions {
		if _, ok := seen[s.ID]; !ok {
			seen[s.ID] = struct{}{}
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// paginateByID slices all by distinct session ID, returning the rows for the
// requested page and the total number of distinct IDs. Input order is
// preserved; all rows sharing an ID travel together.
func paginateByID(all []monitor.SessionMetric, page, perPage int) (rows []monitor.SessionMetric, total int) {
	ids := distinctSessionIDs(all)
	total = len(ids)
	start := (page - 1) * perPage
	end := start + perPage
	if start >= total {
		return nil, total
	}
	if end > total {
		end = total
	}
	pageIDs := map[string]struct{}{}
	for _, id := range ids[start:end] {
		pageIDs[id] = struct{}{}
	}
	for _, s := range all {
		if _, ok := pageIDs[s.ID]; ok {
			rows = append(rows, s)
		}
	}
	return rows, total
}
