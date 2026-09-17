package localserver

import (
	"html/template"
	"net/http"
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
)

var landingTemplate = template.Must(template.New("landing").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
</head>
<body>
<main>
<h1>{{.Title}}</h1>
<p>{{.Body}}</p>
<pre><code>ovdb open</code></pre>
<p>{{.Assistant}}</p>
{{if .Fallback}}<p>{{.Fallback}}</p>{{end}}
</main>
</body>
</html>
`))

// landing is the page for every browser route without a session: it
// explains how to get a login link and exposes no data (REQ:landing-page).
func (s *localServer) landing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	page := struct{ Title, Body, Assistant, Fallback string }{
		Title:     uicopy.T("landing.title", nil),
		Body:      uicopy.T("landing.body", nil),
		Assistant: uicopy.T("landing.assistant", nil),
	}
	if strings.HasPrefix(strings.ToLower(r.Host), "127.0.0.1:") {
		page.Fallback = uicopy.T("landing.fallback_host", nil)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = landingTemplate.Execute(w, page)
}
