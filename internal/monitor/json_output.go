package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// RunJSON loads records, aggregates, optionally filters, and writes JSON to w.
func RunJSON(ctx context.Context, sessionsDir, logsDir, ideBaseDir, cwdFilter, sqliteDBPath string, since time.Duration, session, agent string, w io.Writer) error {
	records, _, err := LoadAll(ctx, sessionsDir, logsDir, ideBaseDir, sqliteDBPath, time.Now().Add(-since), cwdFilter, nil, NewSQLiteCache())
	if err != nil {
		return fmt.Errorf("load records: %w", err)
	}

	dm, err := AggregateDetail(ctx, records, time.Now())
	if err != nil {
		return fmt.Errorf("aggregate: %w", err)
	}

	dm, err = filterJSONMetrics(dm, session, agent)
	if err != nil {
		return err
	}

	b, err := json.MarshalIndent(dm, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	_, err = w.Write(b)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

func filterJSONMetrics(dm DetailedMetrics, session, agent string) (DetailedMetrics, error) {
	switch {
	case session != "" && agent != "":
		return filterJSONMetricsBySessionAndAgent(dm, session, agent)
	case session != "":
		return filterJSONMetricsBySession(dm, session)
	case agent != "":
		return filterJSONMetricsByAgent(dm, agent), nil
	default:
		return dm, nil
	}
}

func filterJSONMetricsBySessionAndAgent(dm DetailedMetrics, session, agent string) (DetailedMetrics, error) {
	// Narrow to exact session+agent match.
	var match []SessionDetail
	for _, s := range dm.Sessions {
		if s.ID == session && s.Agent == agent {
			match = append(match, s)
			break
		}
	}
	if len(match) == 0 {
		return DetailedMetrics{}, fmt.Errorf("no session matches sid=%s agent=%s", session, agent)
	}
	dm.Sessions = match
	dm.Agents = filterJSONAgentsByName(dm.Agents, agent)
	return dm, nil
}

func filterJSONMetricsBySession(dm DetailedMetrics, session string) (DetailedMetrics, error) {
	// Merge all per-agent entries for this session.
	var src []SessionDetail
	for _, s := range dm.Sessions {
		if s.ID == session {
			src = append(src, s)
		}
	}
	if len(src) == 0 {
		return DetailedMetrics{}, fmt.Errorf("no session matches sid=%s", session)
	}
	merged, _ := MergeSessionDetails(src)
	dm.Sessions = []SessionDetail{merged}
	return dm, nil
}

func filterJSONMetricsByAgent(dm DetailedMetrics, agent string) DetailedMetrics {
	// Existing agent-only filter behavior.
	filtered := dm.Sessions[:0:0]
	for _, s := range dm.Sessions {
		if s.Agent == agent {
			filtered = append(filtered, s)
		}
	}
	dm.Sessions = filtered
	dm.Agents = filterJSONAgentsByName(dm.Agents, agent)
	return dm
}

func filterJSONAgentsByName(agents []AgentDetail, name string) []AgentDetail {
	filtered := agents[:0:0]
	for _, a := range agents {
		if a.Name == name {
			filtered = append(filtered, a)
		}
	}
	return filtered
}
