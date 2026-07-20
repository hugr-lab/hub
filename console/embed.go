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
	"os"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Assets returns the embedded SPA build, rooted at dist/.
func Assets() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}

// source resolves the SPA asset filesystem. A non-empty dir serves the build
// from disk (os.DirFS) — the container places the freshly built SPA there and
// sets HUB_CONSOLE_DIR, decoupling the assets from the Go binary. If dir is
// empty, or is set but does not contain an index.html (misconfigured mount), it
// falls back to the embedded build so the console is never silently blank. The
// returned bool reports whether the disk dir was used.
func source(dir string) (fs.FS, bool, error) {
	if dir != "" {
		disk := os.DirFS(dir)
		if _, err := fs.Stat(disk, "index.html"); err == nil {
			return disk, true, nil
		}
	}
	sub, err := Assets()
	return sub, false, err
}

// Handler serves the SPA at prefix (e.g. "/console/") with SPA-fallback: a
// request that maps to a real asset is served as-is; anything else returns
// index.html so the client-side router owns deep links. prefix is stripped
// before the FS lookup. When dir is non-empty (and valid) the assets are read
// from that disk directory; otherwise the embedded build is used. fromDisk
// reports which source won so the caller can log it.
func Handler(prefix, dir string) (h http.Handler, fromDisk bool, err error) {
	sub, fromDisk, err := source(dir)
	if err != nil {
		return nil, false, err
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
	return http.StripPrefix(strip, spa), fromDisk, nil
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
