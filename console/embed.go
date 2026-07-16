// Package console serves the embedded Hugr Hub management console SPA
// (design 009 — management console + embeddable agent chat). The Vite build
// output lives in dist/ and is compiled into the hub binary via go:embed;
// Handler serves it under a URL prefix with SPA-fallback routing so deep links
// and page refreshes resolve to the client-side router.
//
// The dist/ directory ships a committed placeholder index.html so the package
// always compiles; a real `npm run build` (base "/console/", outDir "dist")
// overwrites it with the built shell + hashed assets. See README.md.
package console

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Assets returns the embedded SPA build, rooted at dist/.
func Assets() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}

// Handler serves the embedded SPA at prefix (e.g. "/console/") with
// SPA-fallback: a request that maps to an embedded file is served as-is;
// anything else returns index.html so the client-side router owns deep links.
// prefix is stripped before the FS lookup.
func Handler(prefix string) (http.Handler, error) {
	sub, err := Assets()
	if err != nil {
		return nil, err
	}
	strip := strings.TrimSuffix(prefix, "/")
	fileServer := http.FileServer(http.FS(sub))
	spa := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			serveIndex(w, r, fileServer)
			return
		}
		f, err := sub.Open(name)
		if err != nil {
			// Not an embedded asset → treat as a client-side route.
			serveIndex(w, r, fileServer)
			return
		}
		_ = f.Close()
		fileServer.ServeHTTP(w, r)
	})
	return http.StripPrefix(strip, spa), nil
}

// serveIndex renders the SPA shell (index.html) for a client-side route. The
// shell is never cached so a fresh deploy is picked up immediately; hashed
// assets under it remain long-cacheable via their filenames.
func serveIndex(w http.ResponseWriter, r *http.Request, fileServer http.Handler) {
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/"
	w.Header().Set("Cache-Control", "no-cache")
	fileServer.ServeHTTP(w, r2)
}
