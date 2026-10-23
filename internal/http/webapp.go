package http

import (
	"net/http"
	"path"
	"path/filepath"
	"strings"
)

// SetWebApp serves the built web app (web_root) from dir on the API's
// address, for setups without a separate web server. Call before serving.
func (s *Server) SetWebApp(dir string) { s.webApp = dir }

// webApp serves dir's files as they are, and index.html for every other
// path so the app's own router takes over (like the nginx image's
// try_files).
func webApp(dir string) http.Handler {
	root := http.Dir(dir)
	files := http.FileServer(root)
	index := filepath.Join(dir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := path.Clean("/" + r.URL.Path)
		if f, err := root.Open(p); err == nil {
			st, err := f.Stat()
			f.Close()
			if err == nil && !st.IsDir() && p != "/index.html" {
				if strings.HasPrefix(p, "/_app/immutable/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, index)
	})
}
