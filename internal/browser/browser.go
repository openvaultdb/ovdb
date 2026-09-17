// Package browser opens a URL in the person's default browser with the
// platform's own opener: xdg-open on Linux and the BSDs, open on macOS, and
// url.dll's FileProtocolHandler through rundll32 on Windows (which, unlike
// `cmd /c start`, needs no quoting of & in a URL).
//
// It reports ErrUnavailable instead of trying when there is clearly no way
// to show a browser — no opener on PATH, or a Linux session with no display
// (SSH, containers, CI) — so callers fall back to printing the link
// (spec/features/local-server-and-web-console#REQ:login-links).
package browser

import (
	"errors"
	"os"
	"os/exec"
	goruntime "runtime"
)

// ErrUnavailable means no browser can be launched from this process.
var ErrUnavailable = errors.New("no browser can be opened from here")

// Opener launches browsers. The zero value uses the real environment.
type Opener struct {
	GOOS     string                                  // runtime.GOOS when empty
	Getenv   func(string) string                     // os.Getenv when nil
	LookPath func(string) (string, error)            // exec.LookPath when nil
	Start    func(name string, args ...string) error // starts without waiting when nil
}

// Command is the opener and its arguments for url on goos.
func Command(goos, url string) (name string, args []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		return "xdg-open", []string{url}
	}
}

// Open launches the default browser at url and returns without waiting for it.
func (o Opener) Open(url string) error {
	goos, getenv, lookPath, start := o.GOOS, o.Getenv, o.LookPath, o.Start
	if goos == "" {
		goos = goruntime.GOOS
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if start == nil {
		start = startDetached
	}
	if goos != "darwin" && goos != "windows" && getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
		return ErrUnavailable
	}
	name, args := Command(goos, url)
	if _, err := lookPath(name); err != nil {
		return ErrUnavailable
	}
	return start(name, args...)
}

func startDetached(name string, args ...string) error {
	command := exec.Command(name, args...)
	if err := command.Start(); err != nil {
		return err
	}
	// The opener hands the URL to the browser and exits; nothing waits on it.
	return command.Process.Release()
}
