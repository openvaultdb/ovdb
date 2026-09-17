package localserver

import (
	"embed"
	"html/template"
	"net/http"
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
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
<main class="page">
<p class="brand">{{.Brand}}</p>
<div class="card">
{{end}}
{{define "foot"}}</div>
</main>
</body>
</html>
{{end}}
{{define "landing"}}{{template "head" .}}<h1>{{.Title}}</h1>
<p>{{.Body}}</p>
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
	Brand, Title, Body, Assistant, Fallback string
	Code, Next, Continue                    string
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
	writePage(w, status, "landing", page)
}

// pageAsset serves the landing and login page's stylesheet and script.
func pageAsset(w http.ResponseWriter, r *http.Request) {
	name, contentType := "assets/page.css", "text/css; charset=utf-8"
	if r.URL.Path == submitJSPath {
		name, contentType = "assets/submit.js", "text/javascript; charset=utf-8"
	}
	data, _ := assets.ReadFile(name) // embedded at compile time
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}
