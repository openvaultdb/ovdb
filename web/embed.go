// Package web embeds the Vue console and TODO app that `pnpm -C web build`
// produces, so a released ovdb binary serves them with no Node runtime and
// no separate asset directory to ship alongside it.
//
// dist/ is git-ignored except for dist/.gitkeep, which exists only because
// the go:embed directive below refuses a directory that is not there, so it
// needs something to embed in a clean clone. dist/.gitkeep is not committed
// directly: it is copied there from the tracked public/.gitkeep by every
// `vite build`, because Vite empties dist/ before each build and would
// otherwise delete a placeholder committed straight into it — the same
// technique wb's Astro build uses for hub/web/dist/.gitkeep. Handler then
// serves the friendly not-built page instead of the console. This is S3 of
// spec/features/configuration-parity
// #REQ:increment-zero-spikes, implementing
// spec/features/local-server-and-web-console#REQ:embedded-assets. Modeled on
// sneat-dev/wb's hub/web/embed.go, the reference implementation this pattern
// was proven against.
//
// Handler is not yet mounted by main.go: this package is compiled and
// tested, but not served, until the increment that adds the local server
// (spec/features/local-server-and-web-console).
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
)

// Dist holds every file the Vite build emitted, or only .gitkeep in a clone
// where `pnpm -C web build` has never run.
//
//go:embed all:dist
var Dist embed.FS

// indexPage is the console's build output — its presence is what tells
// Handler the dist is a real build rather than the placeholder.
const indexPage = "index.html"

// Built reports whether a real console build is embedded.
func Built() bool {
	_, err := fs.Stat(distFS, indexPage)
	return err == nil
}

// distFS is dist/ as a filesystem of its own, so a request path maps
// straight onto a file name. fs.Sub only fails on a malformed path and
// "dist" is a literal that go:embed has already resolved, so the error
// cannot happen; it is discarded here rather than turned into an
// unreachable branch.
var distFS, _ = fs.Sub(Dist, "dist")

// Handler serves the embedded console at "/" and the embedded TODO app at
// "/apps/todo/", matching the Vite multi-page build's output layout and
// spec/features/local-server-and-web-console#REQ:route-layout.
func Handler() http.Handler { return handlerFor(distFS, Built()) }

// handlerFor is Handler over an injectable tree, so the built and not-built
// pages are both testable in a checkout that has only one of them.
func handlerFor(files fs.FS, built bool) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !built {
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte(notBuiltPage()))
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+request.URL.Path), "/")
		if name == "" || name == "." {
			name = indexPage
		} else if info, err := fs.Stat(files, name); err == nil && info.IsDir() {
			if !strings.HasSuffix(request.URL.Path, "/") {
				http.Redirect(writer, request, "/"+name+"/", http.StatusFound)
				return
			}
			name = path.Join(name, indexPage)
		}
		content, err := fs.ReadFile(files, name)
		if err != nil {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", contentType(name))
		_, _ = writer.Write(content)
	})
}

// notBuiltPage renders through uicopy.T so its wording lives in
// copy/en.json alongside every other user-facing string
// (spec/features/configuration-parity#REQ:copy-catalogue), naming Homebrew
// and release downloads as spec/features/local-server-and-web-console
// #REQ:embedded-assets requires.
func notBuiltPage() string {
	return uicopy.T("console.not_built.title", nil) + "\n\n" + uicopy.T("console.not_built.body", nil)
}

// contentType maps the handful of extensions the Vite build emits. The Go
// standard library's mime package consults the host's /etc/mime.types,
// which makes the served type vary by machine; a fixed table keeps the
// embedded console byte-identical everywhere.
func contentType(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/vnd.microsoft.icon"
	default:
		return "application/octet-stream"
	}
}
