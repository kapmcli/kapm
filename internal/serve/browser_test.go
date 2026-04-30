package serve_test

import (
	"testing"

	"github.com/kapmcli/kapm/internal/serve"
)

func TestOpenBrowser(t *testing.T) {
	t.Cleanup(serve.OpenBrowserFnForTest(func(string) error { return nil }))

	tests := []struct {
		url     string
		wantErr bool
	}{
		{"http://localhost:8080/", false},
		{"https://example.com/", false},
		{"file:///etc/passwd", true},
		{"javascript:alert(1)", true},
		{"ftp://example.com/", true},
		{"", true},
	}
	for _, tt := range tests {
		err := serve.OpenBrowser(tt.url)
		if (err != nil) != tt.wantErr {
			t.Errorf("OpenBrowser(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
		}
	}
}
