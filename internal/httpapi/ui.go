package httpapi

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"net/http"
	"strconv"
)

//go:generate go run gen_icons.go

//go:embed ui/index.html ui/app.js ui/style.css ui/favicon.svg ui/favicon.ico
var uiFiles embed.FS

// uiPagePolicy loosens the service-wide CSP for the UI page only, and only
// as far as it needs: its own script and stylesheet, fetches to /qr, and
// the generated code shown from a blob: URL. Nothing inline, nothing from
// another origin, no framing.
const uiPagePolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' blob:; " +
	"connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

var uiAssets = []struct {
	path, file, contentType, csp string // an empty csp keeps the service-wide policy
}{
	{"/", "ui/index.html", "text/html; charset=utf-8", uiPagePolicy},
	{"/app.js", "ui/app.js", "text/javascript; charset=utf-8", ""},
	{"/style.css", "ui/style.css", "text/css; charset=utf-8", ""},
	{"/favicon.svg", "ui/favicon.svg", "image/svg+xml", ""},
	{"/favicon.ico", "ui/favicon.ico", "image/x-icon", ""},
}

// registerUI adds the web UI's routes. Each file is served from memory under
// an exact path; there is no file server, so no directory listings, no
// index.html redirect and no path to anything else.
func registerUI(mux *http.ServeMux) {
	for _, a := range uiAssets {
		body, err := uiFiles.ReadFile(a.file)
		if err != nil {
			panic(err) // the file list above is checked by go:embed at build time
		}
		pattern := a.path
		if pattern == "/" {
			pattern = "/{$}" // "/" alone would match every path
		}
		mux.Handle("GET "+pattern, staticFile(body, a.contentType, a.csp))
		mux.HandleFunc(pattern, serveMethodNotAllowed)
	}
}

// staticFile serves body with a content-hash ETag and no-cache, so browsers
// revalidate cheaply and pick up a new build immediately.
func staticFile(body []byte, contentType, csp string) http.Handler {
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if csp != "" {
			h.Set("Content-Security-Policy", csp)
		}
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("ETag", etag)
		h.Set("Cache-Control", "no-cache")
		if etagMatches(r.Header.Values("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		h.Set("Content-Type", contentType)
		h.Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}
