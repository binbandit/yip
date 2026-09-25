package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const placeholder = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>yip</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>body{font:15px/1.55 system-ui,sans-serif;margin:0;display:grid;place-items:center;min-height:100vh;background:#E9F2F3;color:#183A43}
main{max-width:520px;padding:32px;background:#fff;border-radius:16px}code{background:#F3F7F7;padding:2px 6px;border-radius:6px}</style></head>
<body><main><h1>yip hub is running</h1><p>The web client hasn't been built into this binary. Build it with <code>make web</code> and rebuild the hub.</p>
<p>The API is available under <code>/v1</code>.</p></main></body></html>`

// static serves the embedded single-page client, falling back to index.html
// for client-side routes. Hashed assets are cached immutably.
func (s *Server) static() http.Handler {
	if s.opts.Web == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(placeholder))
		})
	}
	files := http.FileServer(s.opts.Web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			http.NotFound(w, r)
			return
		}
		p := path.Clean(r.URL.Path)
		f, err := s.opts.Web.Open(p)
		if err == nil {
			st, _ := f.Stat()
			f.Close()
			if st != nil && !st.IsDir() {
				if strings.HasPrefix(p, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := s.opts.Web.Open("/index.html")
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(placeholder))
			return
		}
		defer index.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		st, _ := index.Stat()
		http.ServeContent(w, r, "index.html", st.ModTime(), index.(readSeeker))
	})
}

type readSeeker interface {
	Read([]byte) (int, error)
	Seek(int64, int) (int64, error)
}

// WebFS adapts an embedded directory for Options.Web, returning nil when the
// client was not built (only a placeholder file present).
func WebFS(fsys fs.FS, dir string) http.FileSystem {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return http.FS(sub)
}
