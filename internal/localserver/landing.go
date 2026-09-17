package localserver

import (
	"embed"
	"html/template"
	"net/http"
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/web"
)

// assets are the files the Go-rendered pages link to. The CSP
// (default-src 'self') refuses inline styles and scripts, so they are files.
//
//go:embed assets/page.css assets/submit.js
var assets embed.FS

// Public page asset paths, under /login so they need no session.
const (
	pageCSSPath   = "/login/page.css"
	submitJSPath  = "/login/submit.js"
	loginPath     = "/login"
	logoutPath    = "/logout"
	signedOutPath = "/signed-out"
	pageTemplates = `
{{define "head"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>{{.Title}}</title>
<link rel="stylesheet" href="` + pageCSSPath + `">
</head>
<body>
<header class="bar"><div class="column"><span class="brand">{{.Brand}}</span></div></header>
<main class="column">
{{if .Notice}}<p class="notice" role="status">{{.Notice}}</p>{{end}}
<div class="card">
{{end}}
{{define "foot"}}</div>
</main>
</body>
</html>
{{end}}
{{define "landing"}}{{template "head" .}}<h1>{{.Title}}</h1>
{{if .App}}<p>{{.App}}</p>
<pre><code>ovdb demo open</code></pre>
{{end}}<p>{{.Body}}</p>
<pre><code>ovdb open</code></pre>
<p>{{.Assistant}}</p>
{{if .Fallback}}<p class="hint">{{.Fallback}}</p>{{end}}
{{template "foot" .}}{{end}}
{{define "login"}}{{template "head" .}}<h1>{{.Title}}</h1>
<form id="login" method="post" action="` + loginPath + `">
<input type="hidden" name="code" value="{{.Code}}">
{{if .Next}}<input type="hidden" name="next" value="{{.Next}}">{{end}}
<noscript><p class="muted">{{.Body}}</p><button type="submit">{{.Continue}}</button></noscript>
</form>
<script src="` + submitJSPath + `"></script>
{{template "foot" .}}{{end}}
`
)

var pages = template.Must(template.New("pages").Parse(pageTemplates))

type pageData struct {
	Brand, Title, Body, Assistant, Fallback, Notice string
	// App is the way back to an app the person was using (the TODO app).
	App string
	Code, Next, Continue                            string
}

func writePage(w http.ResponseWriter, status int, name string, data pageData) {
	data.Brand = uicopy.T("app.name", nil)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = pages.ExecuteTemplate(w, name, data)
}

// landing is the page for every browser route without a session: it
// explains how to get a login link and exposes no data (REQ:landing-page).
func (s *localServer) landing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	writeLanding(w, r, http.StatusOK)
}

func writeLanding(w http.ResponseWriter, r *http.Request, status int) {
	page := pageData{
		Title:     uicopy.T("landing.title", nil),
		Body:      uicopy.T("landing.body", nil),
		Assistant: uicopy.T("landing.assistant", nil),
	}
	if strings.HasPrefix(strings.ToLower(r.Host), "127.0.0.1:") {
		page.Fallback = uicopy.T("landing.fallback_host", nil)
	}
	if r.URL.Path == "/apps/todo" || strings.HasPrefix(r.URL.Path, "/apps/todo/") {
		page.App = uicopy.T("landing.todo_app", nil)
	}
	if r.URL.Path == signedOutPath {
		page.Notice = uicopy.T("landing.signed_out", nil)
	}
	writePage(w, status, "landing", page)
}

// pageAsset serves the landing and login page's stylesheet — the console's
// design tokens followed by the page layout — and script.
func pageAsset(w http.ResponseWriter, r *http.Request) {
	contentType := "text/css; charset=utf-8"
	var data []byte
	if r.URL.Path == submitJSPath {
		contentType = "text/javascript; charset=utf-8"
		data, _ = assets.ReadFile("assets/submit.js") // embedded at compile time
	} else {
		layout, _ := assets.ReadFile("assets/page.css")
		data = append(append(append([]byte{}, web.TokensCSS...), '\n'), layout...)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}
