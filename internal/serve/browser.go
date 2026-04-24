package serve

// OpenBrowser opens url in the user's default browser. The implementation is
// OS-specific (see browser_unix.go and browser_windows.go).
var OpenBrowser = openBrowser
