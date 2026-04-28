package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandleDesignPreview_ParseError verifies that invalid DESIGN.md bytes
// cause the handler to return HTTP 500 with a generic body (no internal detail leakage).
func TestHandleDesignPreview_ParseError(t *testing.T) {
	orig := DesignMDRaw
	t.Cleanup(func() { DesignMDRaw = orig })
	DesignMDRaw = []byte("not a valid DESIGN.md")

	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/design-preview", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Internal Server Error") {
		t.Fatalf("body = %q, want it to contain %q", body, "Internal Server Error")
	}
	if strings.Contains(body, "design parse") || strings.Contains(body, "DESIGN.md") {
		t.Fatalf("body leaks internal detail: %s", body)
	}
}

// TestHandleDesignPreview_OK verifies the happy path renders expected markers.
func TestHandleDesignPreview_OK(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/design-preview", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"Color Palette", "swatch", "#7D56F4", "#04B575"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}
