package main

import (
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestAssets(t *testing.T) {
	assets := loadAssets(fstest.MapFS{
		"assets/index.js":     {Data: []byte("original js")},
		"assets/index.js.br":  {Data: []byte("br")},
		"assets/index.js.gz":  {Data: []byte("gzip!")},
		"assets/index.js.zst": {Data: []byte("zstd")},
		"assets/tiny.css":     {Data: []byte("a{}")},
	})
	for _, c := range []struct {
		path, accept, want, body string
	}{
		{"/assets/index.js", "gzip, deflate, br, zstd", "br", "br"},
		{"/assets/index.js", "gzip, zstd", "zstd", "zstd"},
		{"/assets/index.js", "gzip, br;q=0", "gzip", "gzip!"},
		{"/assets/index.js", "", "", "original js"},
		{"/assets/tiny.css", "br", "", "a{}"},
	} {
		req := httptest.NewRequest("GET", c.path, nil)
		req.Header.Set("Accept-Encoding", c.accept)
		rec := httptest.NewRecorder()
		assets[c.path].serve(rec, req)
		if got := rec.Header().Get("Content-Encoding"); got != c.want || rec.Body.String() != c.body {
			t.Errorf("%s %q: got %q %q, want %q", c.path, c.accept, got, rec.Body, c.want)
		}
	}
	if ct := assets["/assets/index.js"].contentType; ct != "text/javascript; charset=utf-8" {
		t.Errorf("got content type %q", ct)
	}
	if assets["/assets/index.js.br"] != nil {
		t.Error("a compressed copy is served on its own path")
	}
}
