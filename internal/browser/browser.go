// Package browser launches the person's default web browser at a URL. It is
// deliberately small: the TUI's "Open in browser" action (OVDB server
// screen) is its only caller in increment 1c. Increment 1b's web console
// lane may land its own opener for `ovdb open`; if it lands after this one
// merges, the two are meant to be consolidated into one package rather than
// kept as duplicates — see spec/features/local-server-and-web-console
// (REQ:login-links: "ovdb open ... MUST print the links and exit 0" when no
// browser can be launched, which is why Open's error is always a
// print-only-fallback signal to the caller, never a failure to surface as a
// Problem screen).
package browser

import (
	"os/exec"
	"runtime"
)

// Command runs name with args; tests inject a fake to avoid actually
// launching a browser.
var Command = exec.Command

// goos is runtime.GOOS as a variable so tests can drive commandFor for
// every platform without actually running on it.
var goos = runtime.GOOS

// Open starts the platform's browser opener for url and returns
// immediately; it does not wait for the browser to exit. A non-nil error
// means no opener could be started (for example a headless or sandboxed
// environment) — callers fall back to showing the link instead of treating
// this as an operation failure.
func Open(url string) error {
	name, args := commandFor(goos, url)
	return Command(name, args...).Start()
}

// commandFor returns the opener binary and arguments for platform (the
// value of runtime.GOOS), factored out so it is testable without actually
// running anything.
func commandFor(platform, url string) (name string, args []string) {
	switch platform {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		// xdg-open covers Linux and the BSDs; there is no opener to try on
		// a platform with neither a desktop session nor xdg-open, so Open
		// simply fails there and the caller shows the link instead.
		return "xdg-open", []string{url}
	}
}
