package main

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"sync"
)

//go:embed all:client/dist
var dist embed.FS

var ui = func() fs.FS {
	sub, _ := fs.Sub(dist, "client/dist")
	return sub
}()

// index is the page every route serves, nil when the ui wasn't built into this binary
var index = sync.OnceValue(func() []byte {
	b, _ := fs.ReadFile(ui, "index.html")
	return b
})

// assets serves the built files, which have hashes in their names so they never change
func assets(fsys fs.FS) http.Handler {
	// go's built in table has no fonts, and distroless has no /etc/mime.types
	mime.AddExtensionType(".woff2", "font/woff2")
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, err := fs.Stat(fsys, r.URL.Path[1:]); err != nil || f.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		files.ServeHTTP(w, r)
	})
}
