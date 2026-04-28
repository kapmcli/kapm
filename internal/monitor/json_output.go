package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// RunJSON loads records, aggregates, optionally filters, and writes JSON to w.
func RunJSON(ctx context.Context, sessionsDir, logsDir, cwdFilter string, since time.Duration, session, agent string, w io.Writer) error {
	records, _, err := LoadAll(ctx, sessionsDir, logsDir, time.Now().Add(-since), cwdFilter, nil)
	if err != nil {
		return err
	}

	dm, err := AggregateDetail(ctx, records, time.Now())
	if err != nil {
		return err
	}

	switch {
	case session != "" && agent != "":
		// Narrow to exact session+agent match.
		var match []SessionDetail
		for _, s := range dm.Sessions {
			if s.ID == session && s.Agent == agent {
				match = append(match, s)
				break
			}
		}
		if len(match) == 0 {
			return fmt.Errorf("no session matches sid=%s agent=%s", session, agent)
		}
		dm.Sessions = match
		filteredAgents := dm.Agents[:0:0]
		for _, a := range dm.Agents {
			if a.Name == agent {
				filteredAgents = append(filteredAgents, a)
			}
		}
		dm.Agents = filteredAgents

	case session != "":
		// Merge all per-agent entries for this session.
		var src []SessionDetail
		for _, s := range dm.Sessions {
			if s.ID == session {
				src = append(src, s)
			}
		}
		if len(src) == 0 {
			return fmt.Errorf("no session matches sid=%s", session)
		}
		merged, _ := MergeSessionDetails(src)
		dm.Sessions = []SessionDetail{merged}

	case agent != "":
		// Existing agent-only filter behavior.
		filtered := dm.Sessions[:0:0]
		for _, s := range dm.Sessions {
			if s.Agent == agent {
				filtered = append(filtered, s)
			}
		}
		dm.Sessions = filtered
		filteredAgents := dm.Agents[:0:0]
		for _, a := range dm.Agents {
			if a.Name == agent {
				filteredAgents = append(filteredAgents, a)
			}
		}
		dm.Agents = filteredAgents
	}

	b, err := json.MarshalIndent(dm, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
