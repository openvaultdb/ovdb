package browser

import (
	"errors"
	"slices"
	"testing"
)

func TestCommandPerPlatform(t *testing.T) {
	t.Parallel()
	const url = "http://ovdb.localhost:6832/login?code=a&next=%2F"
	for goos, want := range map[string][]string{
		"linux":   {"xdg-open", url},
		"freebsd": {"xdg-open", url},
		"darwin":  {"open", url},
		"windows": {"rundll32", "url.dll,FileProtocolHandler", url},
	} {
		name, args := Command(goos, url)
		if got := append([]string{name}, args...); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", goos, got, want)
		}
	}
}

func TestOpen(t *testing.T) {
	t.Parallel()
	env := func(vars map[string]string) func(string) string { return func(key string) string { return vars[key] } }
	found := func(string) (string, error) { return "/usr/bin/opener", nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }
	for _, tc := range []struct {
		name     string
		opener   Opener
		want     error
		launched bool
	}{
		{"linux with a display", Opener{GOOS: "linux", Getenv: env(map[string]string{"DISPLAY": ":0"}), LookPath: found}, nil, true},
		{"linux on wayland", Opener{GOOS: "linux", Getenv: env(map[string]string{"WAYLAND_DISPLAY": "wayland-0"}), LookPath: found}, nil, true},
		{"linux over ssh", Opener{GOOS: "linux", Getenv: env(nil), LookPath: found}, ErrUnavailable, false},
		{"linux without xdg-open", Opener{GOOS: "linux", Getenv: env(map[string]string{"DISPLAY": ":0"}), LookPath: missing}, ErrUnavailable, false},
		{"macOS", Opener{GOOS: "darwin", Getenv: env(nil), LookPath: found}, nil, true},
		{"windows", Opener{GOOS: "windows", Getenv: env(nil), LookPath: found}, nil, true},
	} {
		launched := false
		tc.opener.Start = func(string, ...string) error { launched = true; return nil }
		if err := tc.opener.Open("http://127.0.0.1:6832/"); !errors.Is(err, tc.want) || launched != tc.launched {
			t.Errorf("%s: err %v launched %t", tc.name, err, launched)
		}
	}
	failing := Opener{GOOS: "darwin", LookPath: found, Start: func(string, ...string) error { return errors.New("boom") }}
	if err := failing.Open("http://x"); err == nil || errors.Is(err, ErrUnavailable) {
		t.Errorf("start failure = %v", err)
	}
}
