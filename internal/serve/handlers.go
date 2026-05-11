package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/kapmcli/kapm/internal/kirocliusage"
	"github.com/kapmcli/kapm/internal/monitor"
	"github.com/kapmcli/kapm/internal/stylegen"
)

type overviewSummary struct {
	monitor.Metrics
	KiroUsage        *kirocliusage.Usage
	KiroUsageEnabled bool
	KiroUsageChecked bool
}

func newOverviewSummary(metrics monitor.Metrics, usage *kirocliusage.Usage, usageEnabled, usageChecked bool) overviewSummary {
	return overviewSummary{Metrics: metrics, KiroUsage: usage, KiroUsageEnabled: usageEnabled, KiroUsageChecked: usageChecked}
}

// agentLink is a {agent, URL} pair rendered next to a merged session view.
// URL construction lives here so internal/monitor stays URL-free.
type agentLink struct {
	Agent string
	URL   string
}

// buildAgentLinks returns URL links for each agent ref, escaping the agent
// name so values like "(unknown)" survive a URL round-trip.
func buildAgentLinks(id string, refs []monitor.AgentRef) []agentLink {
	links := make([]agentLink, 0, len(refs))
	for _, r := range refs {
		links = append(links, agentLink{
			Agent: r.Agent,
			URL:   "/sessions/" + id + "/" + url.PathEscape(r.Agent),
		})
	}
	return links
}

// currentUpdatedAt returns the header "updated:" timestamp. If KAPM_UPDATED_AT
// is set it takes precedence (used by vhs-test and playwright for stable
// goldens); otherwise the current wall-clock time is formatted as HH:MM:SS.
func currentUpdatedAt() string {
	if v := os.Getenv("KAPM_UPDATED_AT"); v != "" {
		return v
	}
	return time.Now().Format("15:04:05")
}

// renderPage executes tmpl's "layout" (or "content" + OOB "nav" for htmx
// requests so the active nav link updates) and writes status on success.
func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, status int, tmpl *template.Template, data map[string]any) {
	if _, ok := data["Nav"]; !ok {
		data["Nav"] = navItems
	}
	data["UpdatedAt"] = currentUpdatedAt()
	var buf bytes.Buffer
	isHX := r != nil && r.Header.Get("HX-Request") == "true"
	if isHX {
		if err := tmpl.ExecuteTemplate(&buf, "content", data); err != nil {
			s.handleError(w, r, fmt.Errorf("render page content template %q: %w", tmpl.Name(), err), http.StatusInternalServerError)
			return
		}
		// Out-of-band swap updates the nav's active link alongside #content.
		buf.WriteString(`<nav id="main-nav" hx-swap-oob="true">`)
		if err := tmpl.ExecuteTemplate(&buf, "nav", data); err != nil {
			s.handleError(w, r, fmt.Errorf("render page nav template: %w", err), http.StatusInternalServerError)
			return
		}
		buf.WriteString(`</nav>`)
		// OOB swap for the header's updated-at stamp so htmx navigation
		// refreshes the timestamp without a full reload.
		fmt.Fprintf(&buf, `<span id="updated-at" class="updated-at" hx-swap-oob="true">updated: %s</span>`, html.EscapeString(fmt.Sprint(data["UpdatedAt"])))
		// OOB swap for the browser tab title on htmx navigation.
		if titleStr, _ := data["Title"].(string); titleStr != "" {
			fmt.Fprintf(&buf, `<title hx-swap-oob="true">%s — kapm</title>`, html.EscapeString(titleStr))
		}
	} else {
		if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
			s.handleError(w, r, err, http.StatusInternalServerError)
			return
		}
	}
	s.writeHTML(w, r, status, buf.Bytes())
}

func (s *Server) writeHTML(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		s.logWriteFailure(r, "html response", err)
	}
}

// handleDashboard renders the Overview page from embedded templates.
// Overview.Sessions is capped to dashboardSessionLimit distinct session IDs
// (paginateByID page=1 semantics) so the Recent Sessions panel stays bounded.
// Depends on computeDashboardSessions returning rows in LastActivity-desc order
// so that page 1 of 50 is the most-recent 50 distinct IDs.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	loaded, err := s.loadMetrics(r.Context())
	if err != nil {
		s.handleError(w, r, err, http.StatusInternalServerError)
		return
	}
	capped, _ := paginateByID(loaded.sessions, 1, dashboardSessionLimit)
	tableOverview := loaded.dm.Overview
	tableOverview.Sessions = capped
	usage, usageChecked := s.currentKiroUsage(s.now())
	overviewJSON, err := marshalForTemplate(tableOverview)
	if err != nil {
		s.handleError(w, r, err, http.StatusInternalServerError)
		return
	}
	s.renderPage(w, r, http.StatusOK, overviewTmpl, map[string]any{
		"Title":        "Overview",
		"Active":       "overview",
		"Overview":     tableOverview,
		"Summary":      newOverviewSummary(loaded.dm.Overview, usage, s.kiroUsageRead != nil, usageChecked),
		"Skills":       loaded.dm.Skills,
		"OverviewJSON": overviewJSON,
	})
}

// handleSessions renders the sessions list page with ?page=N pagination.
// Distinct session IDs are paginated at sessionsPerPage per page.
// Invalid or out-of-range page values are clamped: <1 → 1, >totalPages → totalPages.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	loaded, err := s.loadMetrics(r.Context())
	if err != nil {
		s.handleError(w, r, err, http.StatusInternalServerError)
		return
	}
	// Parse ?page= — default 1, clamp <1 to 1.
	requested := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n >= 1 {
			requested = n
		}
	}

	all := loaded.dm.Overview.Sessions
	rows, total := paginateByID(all, requested, sessionsPerPage)

	totalPages := max((total+sessionsPerPage-1)/sessionsPerPage, 1)
	currentPage := requested
	if currentPage > totalPages {
		currentPage = totalPages
		rows, _ = paginateByID(all, currentPage, sessionsPerPage)
	}

	s.renderPage(w, r, http.StatusOK, sessionsTmpl, map[string]any{
		"Title":      "Sessions",
		"Active":     "sessions",
		"Sessions":   rows,
		"Page":       currentPage,
		"TotalPages": totalPages,
		"HasPrev":    currentPage > 1,
		"HasNext":    currentPage < totalPages,
	})
}

// handleSessionDetail serves the merged (all-agents) session detail page;
// 404 if no SessionDetail has that id.
func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	loaded, err := s.loadMetrics(r.Context())
	if err != nil {
		s.handleError(w, r, err, http.StatusInternalServerError)
		return
	}
	matches := sessionDetailsByID(loaded.dm.Sessions, id)
	if len(matches) == 0 {
		s.handleNotFound(w, r)
		return
	}
	merged, refs := monitor.MergeSessionDetails(matches)
	s.renderPage(w, r, http.StatusOK, sessionDetailTmpl, map[string]any{
		"Title":      "Session " + id,
		"Active":     "sessions",
		"Session":    merged,
		"AgentLinks": buildAgentLinks(id, refs),
		"SelfURL":    "/sessions/" + id,
	})
}

// handleSessionAgentDetail serves the per-agent session detail page.
// Returns 400 if the agent segment cannot be URL-decoded, 404 if the
// (id, agent) pair is unknown.
func (s *Server) handleSessionAgentDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rawAgent := r.PathValue("agent")
	agent, err := url.PathUnescape(rawAgent)
	if err != nil {
		s.handleError(w, r, fmt.Errorf("serve decode agent %q: %w", rawAgent, err), http.StatusBadRequest)
		return
	}
	loaded, err := s.loadMetrics(r.Context())
	if err != nil {
		s.handleError(w, r, err, http.StatusInternalServerError)
		return
	}
	target, others, ok := sessionDetailByIDAndAgent(loaded.dm.Sessions, id, agent)
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	s.renderPage(w, r, http.StatusOK, sessionDetailTmpl, map[string]any{
		"Title":      "Session " + id + " / " + agent,
		"Active":     "sessions",
		"Session":    target,
		"AgentLinks": buildAgentLinks(id, others),
		"SelfURL":    "/sessions/" + id + "/" + url.PathEscape(agent),
	})
}

// handleAgents renders the agents list page.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	loaded, err := s.loadMetrics(r.Context())
	if err != nil {
		s.handleError(w, r, err, http.StatusInternalServerError)
		return
	}
	s.renderPage(w, r, http.StatusOK, agentsTmpl, map[string]any{
		"Title":  "Agents",
		"Active": "agents",
		"Agents": loaded.dm.Agents,
	})
}

// handleAgentDetail serves the agent detail page; 404 if the name is unknown.
func (s *Server) handleAgentDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	loaded, err := s.loadMetrics(r.Context())
	if err != nil {
		s.handleError(w, r, err, http.StatusInternalServerError)
		return
	}
	agent, ok := agentDetailByName(loaded.dm.Agents, name)
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	s.renderPage(w, r, http.StatusOK, agentDetailTmpl, map[string]any{
		"Title":  "Agent " + name,
		"Active": "agents",
		"Agent":  agent,
	})
}

// handleTools renders the tools list page.
func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	loaded, err := s.loadMetrics(r.Context())
	if err != nil {
		s.handleError(w, r, err, http.StatusInternalServerError)
		return
	}
	s.renderPage(w, r, http.StatusOK, toolsTmpl, map[string]any{
		"Title":  "Tools",
		"Active": "tools",
		"Tools":  loaded.dm.Tools,
	})
}

// toolDetailVM is the JSON payload injected into /tools/{name} for echarts.
type toolDetailVM struct {
	Timeseries []monitor.TimeseriesPoint `json:"timeseries"`
	Patterns   []monitor.PatternCount    `json:"patterns"`
}

// buildToolDetailVM merges RecentCalls and Errors then aggregates timeseries and patterns.
func buildToolDetailVM(td monitor.ToolDetail, now time.Time) toolDetailVM {
	all := make([]monitor.ToolCall, 0, len(td.RecentCalls)+len(td.Errors))
	all = append(all, td.RecentCalls...)
	all = append(all, td.Errors...)
	return toolDetailVM{
		Timeseries: monitor.AggregateToolTimeseries(all, now),
		Patterns:   monitor.AggregateToolInputPatterns(all, 10),
	}
}

// handleToolDetail serves the tool detail page; 404 if the name is unknown.
func (s *Server) handleToolDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	loaded, err := s.loadMetrics(r.Context())
	if err != nil {
		s.handleError(w, r, err, http.StatusInternalServerError)
		return
	}
	tool, ok := toolDetailByName(loaded.dm.Tools, name)
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	toolDetailJSON, err := marshalForTemplate(buildToolDetailVM(tool, s.now()))
	if err != nil {
		s.handleError(w, r, err, http.StatusInternalServerError)
		return
	}
	s.renderPage(w, r, http.StatusOK, toolDetailTmpl, map[string]any{
		"Title":          "Tool " + tool.Name,
		"Active":         "tools",
		"Tool":           tool,
		"ToolDetailJSON": toolDetailJSON,
	})
}

// handleSkills renders the skills list page.
func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	loaded, err := s.loadMetrics(r.Context())
	if err != nil {
		s.handleError(w, r, err, http.StatusInternalServerError)
		return
	}
	s.renderPage(w, r, http.StatusOK, skillsTmpl, map[string]any{
		"Title":  "Skills",
		"Active": "skills",
		"Skills": loaded.dm.Skills,
	})
}

// colorEntry is a {name, hex} pair rendered as a swatch on /design-preview.
type colorEntry struct {
	Name string
	Hex  string
}

// handleDesignPreview renders a standalone visualization of the color tokens
// defined in the embedded DESIGN.md. It does not depend on runtime metrics
// or the production style.css.
func (s *Server) handleDesignPreview(w http.ResponseWriter, r *http.Request) {
	d, err := stylegen.ParseDesignMD(DesignMDRaw)
	if err != nil {
		s.handleError(w, r, fmt.Errorf("design parse: %w", err), http.StatusInternalServerError)
		return
	}
	entries := make([]colorEntry, 0, len(stylegen.ColorKeys))
	for _, k := range stylegen.ColorKeys {
		entries = append(entries, colorEntry{Name: k, Hex: d.Colors[k]})
	}
	var buf bytes.Buffer
	if err := designPreviewTmpl.ExecuteTemplate(&buf, "design_preview.html", map[string]any{
		"Design":       d,
		"ColorEntries": entries,
	}); err != nil {
		s.handleError(w, r, fmt.Errorf("design render: %w", err), http.StatusInternalServerError)
		return
	}
	s.writeHTML(w, r, http.StatusOK, buf.Bytes())
}

// handleAPIMetrics returns the full DetailedMetrics (optionally filtered) as JSON.
func (s *Server) handleAPIMetrics(w http.ResponseWriter, r *http.Request) {
	loaded, err := s.loadMetrics(r.Context())
	if err != nil {
		s.handleError(w, r, err, http.StatusInternalServerError)
		return
	}
	dm := loaded.dm

	// Optional filters: ?session=<id> or ?agent=<name> narrows the response.
	if sid := r.URL.Query().Get("session"); sid != "" {
		dm = filterBySession(dm, sid)
	} else if ag := r.URL.Query().Get("agent"); ag != "" {
		dm = filterByAgent(dm, ag)
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(dm); err != nil {
		s.handleError(w, r, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

// handleError logs the full error via slog and writes a generic status response.
// Internal details never reach the client.
func (s *Server) handleError(w http.ResponseWriter, r *http.Request, err error, status int) {
	slog.Warn("serve http error",
		"method", r.Method,
		"path", r.URL.Path,
		"status", status,
		"err", err,
	)
	// Attempt styled error page. Fall back to plain text if template
	// rendering itself fails (avoids infinite recursion since renderPage
	// calls handleError on template errors).
	var buf bytes.Buffer
	data := map[string]any{
		"Title":   http.StatusText(status),
		"Active":  "",
		"Status":  status,
		"Heading": http.StatusText(status),
		"Message": http.StatusText(status),
	}
	if tmplErr := errorTmpl.ExecuteTemplate(&buf, "layout", data); tmplErr == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = buf.WriteTo(w)
		return
	}
	http.Error(w, http.StatusText(status), status)
}

// handleNotFound renders the error page with HTTP 404.
func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, r, http.StatusNotFound, errorTmpl, map[string]any{
		"Title":   "Not Found",
		"Active":  "",
		"Status":  404,
		"Heading": "Not Found",
		"Message": "The requested resource was not found.",
	})
}
