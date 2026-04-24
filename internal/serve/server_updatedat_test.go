package serve

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// full-load response must include the updated-at span with an HH:MM:SS value.
func TestRenderPage_UpdatedAt_FullLoad(t *testing.T) {
	t.Setenv("KAPM_UPDATED_AT", "")
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	re := regexp.MustCompile(`id="updated-at"[^>]*>updated: \d{2}:\d{2}:\d{2}`)
	if !re.MatchString(rr.Body.String()) {
		t.Fatalf("full-load response missing updated-at span: %s", rr.Body.String())
	}
}

// htmx-navigation response must include an OOB updated-at span so the header
// timestamp refreshes without a full reload.
func TestRenderPage_UpdatedAt_HTMXNav(t *testing.T) {
	t.Setenv("KAPM_UPDATED_AT", "")
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	re := regexp.MustCompile(`<span id="updated-at"[^>]*hx-swap-oob="true"[^>]*>updated: \d{2}:\d{2}:\d{2}</span>`)
	if !re.MatchString(body) {
		t.Fatalf("htmx response missing OOB updated-at span: %s", body)
	}
}

// malicious KAPM_UPDATED_AT must be HTML-escaped in the htmx OOB swap path.
func TestRenderPage_UpdatedAt_HTMXNav_Escapes(t *testing.T) {
	t.Setenv("KAPM_UPDATED_AT", "<script>alert(1)</script>")
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, `updated: &lt;script&gt;alert(1)&lt;/script&gt;</span>`) {
		t.Errorf("escaped entity not found in updated-at span: %s", body)
	}
	if strings.Contains(body, `updated: <script>`) {
		t.Errorf("raw <script> tag found in updated-at span: %s", body)
	}
}

// KAPM_UPDATED_AT env var must override the live clock so goldens stay stable.
func TestRenderPage_UpdatedAt_EnvOverride(t *testing.T) {
	t.Setenv("KAPM_UPDATED_AT", "12:00:00")
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if !strings.Contains(rr.Body.String(), `updated: 12:00:00`) {
		t.Fatalf("env override not honored: %s", rr.Body.String())
	}
}
