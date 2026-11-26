package main

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
)

//go:embed all:client/dist
var dist embed.FS

var uiAssets = sync.OnceValue(func() map[string]*asset {
	sub, _ := fs.Sub(dist, "client/dist")
	return loadAssets(sub)
})

type asset struct {
	contentType string
	// encoded holds the body for each encoding, and "" for none
	encoded map[string][]byte
}

// the build writes compressed copies next to each file, see client/vite.config.ts
var encodingExts = map[string]string{".br": "br", ".gz": "gzip", ".zst": "zstd"}

func loadAssets(fsys fs.FS) map[string]*asset {
	// go's built in table has no fonts, and distroless has no /etc/mime.types
	mime.AddExtensionType(".woff2", "font/woff2")
	assets := map[string]*asset{}
	get := func(name string) *asset {
		if assets[name] == nil {
			assets[name] = &asset{contentType: mime.TypeByExtension(path.Ext(name)), encoded: map[string][]byte{}}
		}
		return assets[name]
	}
	fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		ext := path.Ext(name)
		if enc, ok := encodingExts[ext]; ok {
			get("/" + strings.TrimSuffix(name, ext)).encoded[enc] = data
		} else {
			get("/" + name).encoded[""] = data
		}
		return nil
	})
	return assets
}

// serve writes the smallest encoding the client takes
func (a *asset) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", a.contentType)
	if len(a.encoded) > 1 {
		w.Header().Add("Vary", "Accept-Encoding")
	}
	accepted := acceptedEncodings(r.Header.Get("Accept-Encoding"))
	best := ""
	for enc, body := range a.encoded {
		if accepted[enc] && len(body) < len(a.encoded[best]) {
			best = enc
		}
	}
	if best != "" {
		w.Header().Set("Content-Encoding", best)
	}
	if r.Method != http.MethodHead {
		w.Write(a.encoded[best])
	}
}
