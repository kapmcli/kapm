package serve

import (
	"fmt"
	"net/url"
)

// openBrowserFn is the platform-specific opener; overridable in tests.
var openBrowserFn = openBrowser

// OpenBrowser opens the given HTTP(S) URL in the user's default browser.
var OpenBrowser = func(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("browser open: invalid URL %q: %w", rawURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("browser open: unsupported scheme %q (want http or https)", u.Scheme)
	}
	return openBrowserFn(rawURL)
}
