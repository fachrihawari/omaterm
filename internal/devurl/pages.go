package devurl

import (
	"html/template"
	"net/http"
)

// pageData is what the proxy's own pages show.
type pageData struct {
	Route  Route   // the not-running page: the route that is down
	Host   string  // the unknown-host page: the host asked for
	Routes []Route // the unknown-host page: the hosts there are
}

// pageStyle is shared by the pages, which load nothing from outside. The
// page rises in as omaterm's own pages do, unless less motion is asked.
const pageStyle = `<style>
:root{color-scheme:light dark;--bg:#fafaf9;--fg:#1c1917;--dim:#78716c;--card:#fff;--line:#e7e5e4;--accent:#b45309}
@media (prefers-color-scheme:dark){:root{--bg:#1c1917;--fg:#f5f5f4;--dim:#a8a29e;--card:#292524;--line:#44403c;--accent:#fbbf24}}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,sans-serif}
main{box-sizing:border-box;width:min(32rem,100%);padding:2rem 1.25rem}
h1{font-size:1.4rem;margin:0 0 .5rem}
p{margin:.25rem 0;color:var(--dim)}
code{font:.9em ui-monospace,monospace;color:var(--accent)}
ul{list-style:none;margin:1rem 0 0;padding:0;border:1px solid var(--line);border-radius:.5rem;background:var(--card)}
li{display:flex;justify-content:space-between;gap:1rem;padding:.6rem .9rem;border-top:1px solid var(--line)}
li:first-child{border-top:0}
li span{color:var(--dim)}
li{transition:background-color 120ms}
li:hover{background:color-mix(in srgb,var(--line) 40%,transparent)}
main{animation:rise 360ms cubic-bezier(.2,.8,.3,1) both}
@keyframes rise{from{opacity:0;transform:translateY(8px)}}
@media (prefers-reduced-motion:reduce){main{animation:none}li{transition:none}}
</style>`

var notRunningPage = template.Must(template.New("notRunning").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Route.Process}} isn't running</title>` + pageStyle + `</head>
<body><main>
<h1>{{.Route.Process}} isn't running</h1>
<p>Start it in omaterm, in the project <code>{{.Route.Project}}</code>.</p>
<p>Status: {{if .Route.Status}}{{.Route.Status}}{{else}}stopped{{end}}</p>
</main></body></html>`))

var unknownHostPage = template.Must(template.New("unknownHost").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>No process at {{.Host}}</title>` + pageStyle + `</head>
<body><main>
<h1>No process at <code>{{.Host}}</code></h1>
{{if .Routes}}<p>These are served by omaterm:</p>
<ul>{{range .Routes}}<li><code>{{.Host}}</code><span>{{.Process}} in {{.Project}}</span></li>{{end}}</ul>
{{else}}<p>No process has a URL yet: give one a <code>url</code> in omaterm.yml.</p>{{end}}
</main></body></html>`))

// writePage answers with one of the proxy's own pages.
func writePage(w http.ResponseWriter, code int, t *template.Template, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	t.Execute(w, data)
}
