package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const noClientPage = `<!doctype html><html><head><meta charset="utf-8"><title>HomeStreaming</title></head>
<body style="font-family:sans-serif;padding:2rem"><h1>HomeStreaming</h1>
<p>The server is running, but this build does not include the web client. Build it with <code>make web</code> and rebuild the server.</p></body></html>`

// serveWeb serves the embedded single-page web client.
func (s *Server) serveWeb(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		notFound(w, "no such endpoint")
		return
	}
	if s.WebFS == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(noClientPage))
		return
	}
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	st, err := fs.Stat(s.WebFS, p)
	if err != nil || st.IsDir() {
		// SPA fallback for extension-less paths.
		if path.Ext(p) != "" {
			http.NotFound(w, r)
			return
		}
		p = "index.html"
		if _, err := fs.Stat(s.WebFS, p); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(noClientPage))
			return
		}
	}
	switch {
	case strings.HasPrefix(p, "assets/"):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	case p == "sw.js" || p == "index.html" || p == "manifest.webmanifest":
		w.Header().Set("Cache-Control", "no-cache")
	default:
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	if p == "manifest.webmanifest" {
		w.Header().Set("Content-Type", "application/manifest+json")
	}
	http.ServeFileFS(w, r, s.WebFS, p)
}
