package web

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func get(t *testing.T, handler http.Handler, target string) *http.Response {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder.Result()
}

func bodyOf(t *testing.T, handler http.Handler, target string) string {
	t.Helper()
	response := get(t, handler, target)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s = %s", target, response.Status)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}
	return string(body)
}

func builtTree() fs.FS {
	return fstest.MapFS{
		"index.html":             {Data: []byte(`<main data-app="console"></main>`)},
		"apps/todo/index.html":   {Data: []byte(`<main data-app="todo"></main>`)},
		"assets/console-abc.js":  {Data: []byte("export {}")},
		"assets/console-abc.css": {Data: []byte(":root{}")},
	}
}

func TestHandlerServesTheConsoleAtRoot(t *testing.T) {
	handler := handlerFor(builtTree(), true)
	body := bodyOf(t, handler, "/")
	if !strings.Contains(body, `data-app="console"`) {
		t.Fatalf("body = %q, want the console placeholder", body)
	}
}

func TestHandlerServesTheTodoAppUnderApps(t *testing.T) {
	handler := handlerFor(builtTree(), true)
	body := bodyOf(t, handler, "/apps/todo/")
	if !strings.Contains(body, `data-app="todo"`) {
		t.Fatalf("body = %q, want the TODO app placeholder", body)
	}
}

func TestHandlerRedirectsToTheTrailingSlashForm(t *testing.T) {
	handler := handlerFor(builtTree(), true)
	response := get(t, handler, "/apps/todo")
	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != "/apps/todo/" {
		t.Fatalf("status = %s, location = %q", response.Status, response.Header.Get("Location"))
	}
}

func TestHandlerServesHashedAssetsWithTheirContentType(t *testing.T) {
	handler := handlerFor(builtTree(), true)
	response := get(t, handler, "/assets/console-abc.js")
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Fatalf("status = %s, content type = %q", response.Status, response.Header.Get("Content-Type"))
	}
}

func TestHandlerReportsAnUnknownFileAsNotFound(t *testing.T) {
	for _, target := range []string{"/nothing/here.html", "/assets/missing.js"} {
		if response := get(t, handlerFor(builtTree(), true), target); response.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %s", target, response.Status)
		}
	}
}

// AC:routes: client-side routes fall back to their app's index.html, and
// hashed assets are cacheable while pages are revalidated.
func TestHandlerFallsBackToTheAppForClientRoutes(t *testing.T) {
	handler := handlerFor(builtTree(), true)
	for target, want := range map[string]string{
		"/settings":            `data-app="console"`,
		"/server":              `data-app="console"`,
		"/apps/todo/lists/abc": `data-app="todo"`,
	} {
		if body := bodyOf(t, handler, target); !strings.Contains(body, want) {
			t.Errorf("%s = %q, want %s", target, body, want)
		}
	}
	if got := get(t, handler, "/assets/console-abc.js").Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("asset Cache-Control = %q", got)
	}
	if got := get(t, handler, "/settings").Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("page Cache-Control = %q", got)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/settings", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /settings = %d", recorder.Code)
	}
}

// TestHandlerExplainsAnUnbuiltConsole is what an `ovdb` built with
// `go install` (no `pnpm -C web build`) serves — AC:not-built-fallback of
// spec/features/local-server-and-web-console. It must name Homebrew and
// release downloads rather than 404 or show a blank page.
func TestHandlerExplainsAnUnbuiltConsole(t *testing.T) {
	handler := handlerFor(fstest.MapFS{".gitkeep": {}}, false)
	response := get(t, handler, "/")
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("status = %s, content type = %q", response.Status, response.Header.Get("Content-Type"))
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	for _, want := range []string{"isn't built", "Homebrew", "releases"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body %q does not mention %q", body, want)
		}
	}
}

// TestEmbeddedDistIsAlwaysPresent is the guard on the go:embed placeholder,
// and — once `pnpm -C web build` has run — the Go test asserting the
// embedded FS actually serves index.html (S3's pass criterion). In a clean
// clone it only has dist/.gitkeep, and Built() must agree with what "/"
// actually serves either way.
func TestEmbeddedDistIsAlwaysPresent(t *testing.T) {
	entries, err := fs.ReadDir(distFS, ".")
	if err != nil || len(entries) == 0 {
		t.Fatalf("embedded dist = %v, %v", entries, err)
	}
	response := get(t, Handler(), "/")
	htmlServed := response.Header.Get("Content-Type") == "text/html; charset=utf-8"
	if Built() != htmlServed {
		t.Fatalf("Built() = %t but \"/\" returned content type %q", Built(), response.Header.Get("Content-Type"))
	}
	if Built() {
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if response.StatusCode != http.StatusOK || len(body) == 0 {
			t.Fatalf("built console served status %s with %d bytes", response.Status, len(body))
		}
	}
}

func TestContentTypeCoversEveryExtensionTheBuildEmits(t *testing.T) {
	for name, want := range map[string]string{
		"a.html":  "text/html; charset=utf-8",
		"a.css":   "text/css; charset=utf-8",
		"a.js":    "text/javascript; charset=utf-8",
		"a.mjs":   "text/javascript; charset=utf-8",
		"a.json":  "application/json",
		"a.svg":   "image/svg+xml",
		"a.woff":  "font/woff",
		"a.woff2": "font/woff2",
		"a.png":   "image/png",
		"a.ico":   "image/vnd.microsoft.icon",
		"a.bin":   "application/octet-stream",
	} {
		if got := contentType(name); got != want {
			t.Fatalf("contentType(%q) = %q, want %q", name, got, want)
		}
	}
}
