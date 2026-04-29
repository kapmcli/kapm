package monitor

// touchSessionState looks up or creates the session keyed by (session, agent),
// then updates cwd and end timestamp from the record.
func touchSessionState(st *aggState, r MergedRecord) *sessionState {
	agent := r.Agent
	if agent == "" {
		agent = "(unknown)" // sentinel: uses chars outside [A-Za-z0-9._-] so it cannot collide with a real agent
	}
	key := compositeKey(r.SessionID, agent)
	s, ok := st.sessions[key]
	if !ok {
		ts := recordSortTs(r)
		s = &sessionState{id: r.SessionID, agent: agent, cwd: r.Cwd, start: ts, end: ts}
		st.sessions[key] = s
	}
	if r.Cwd != "" {
		s.cwd = r.Cwd
	}
	ts := recordSortTs(r)
	if ts.After(s.end) {
		s.end = ts
	}
	if r.UpdatedAt.After(s.end) {
		s.end = r.UpdatedAt
	}
	return s
}

// compositeKey returns the (sid, agent) aggregation key used to uniquely
// identify a session-per-agent. The sid is a UUID ([a-zA-Z0-9-]+), so "|" is
// a safe separator that will not appear in either component.
func compositeKey(sid, agent string) string {
	return sid + "|" + agent
}
