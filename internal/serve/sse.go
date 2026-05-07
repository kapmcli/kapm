package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/kapmcli/kapm/internal/kirocliusage"
)

// defaultMaxSSE is the default cap for concurrent SSE connections.
const defaultMaxSSE int32 = 64

const sseStreamInterval = 5 * time.Second

func (s *Server) logWriteFailure(r *http.Request, context string, err error) {
	attrs := []any{"context", context, "err", err}
	if r != nil {
		attrs = append(attrs, "method", r.Method, "path", r.URL.Path)
	}
	slog.Warn("serve write failed", attrs...)
}

// handleSSE streams Overview summaries (not full DetailedMetrics) every 5s.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	if s.sseCount.Add(1) > s.sseMax {
		s.sseCount.Add(-1)
		w.Header().Set("Retry-After", "1")
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
		return
	}
	defer s.sseCount.Add(-1)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "sse not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	if !s.sendOverview(w, flusher, r) {
		return
	}

	ticker := time.NewTicker(sseStreamInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if !s.sendOverview(w, flusher, r) {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

// buildSSEFrames computes SSE payload bytes from loaded metrics.
func buildSSEFrames(lm loadedMetrics, usage *kirocliusage.Usage, usageEnabled, usageChecked bool) (summaryHTML, overviewJSON []byte, err error) {
	var htmlBuf bytes.Buffer
	if err := overviewTmpl.ExecuteTemplate(&htmlBuf, "summary-cards", newOverviewSummary(lm.dm.Overview, usage, usageEnabled, usageChecked)); err != nil {
		return nil, nil, fmt.Errorf("serve sse render summary: %w", err)
	}
	summaryHTML = bytes.ReplaceAll(htmlBuf.Bytes(), []byte("\n"), []byte(" "))

	overviewJSON, err = json.Marshal(lm.dm.Overview)
	if err != nil {
		return nil, nil, fmt.Errorf("serve sse marshal overview: %w", err)
	}
	return summaryHTML, overviewJSON, nil
}

// sendOverview writes SSE frames with the current overview snapshot:
//   - `event: summary` with rendered summary-cards HTML (for htmx sse-swap)
//   - `event: overview` with Overview JSON (for ECharts update in client JS)
func (s *Server) sendOverview(w io.Writer, flusher http.Flusher, r *http.Request) bool {
	lm, err := s.loadMetrics(r.Context())
	if err != nil {
		slog.Warn("serve sse load metrics", "err", err)
		return false
	}
	usage, usageChecked := s.currentKiroUsage(s.now())

	htmlFrame, jsonFrame, err := buildSSEFrames(lm, usage, s.kiroUsageRead != nil, usageChecked)
	if err != nil {
		slog.Warn("serve sse build frames", "err", err)
		return false
	}

	if _, err := fmt.Fprintf(w, "event: summary\ndata: %s\n\n", htmlFrame); err != nil {
		s.logWriteFailure(r, "sse summary", fmt.Errorf("send sse summary frame: %w", err))
		return false
	}
	if _, err := fmt.Fprintf(w, "event: overview\ndata: %s\n\n", jsonFrame); err != nil {
		s.logWriteFailure(r, "sse overview", fmt.Errorf("send sse overview frame: %w", err))
		return false
	}
	// Flush error is intentionally ignored: the SSE connection will be
	// detected as broken on the next write cycle and closed gracefully.
	flusher.Flush()
	return true
}
