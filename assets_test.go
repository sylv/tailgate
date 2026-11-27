package main

import (
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestAssets(t *testing.T) {
	h := assets(fstest.MapFS{"assets/index.js": {Data: []byte("js")}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/assets/index.js", nil))
	if rec.Code != 200 || rec.Body.String() != "js" || rec.Header().Get("Content-Type") != "text/javascript; charset=utf-8" || rec.Header().Get("Cache-Control") == "" {
		t.Errorf("got %d %q %v", rec.Code, rec.Body, rec.Header())
	}
	// a missing file or the folder isn't cached for good
	for _, path := range []string{"/assets/gone.js", "/assets/"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 404 || rec.Header().Get("Cache-Control") != "" {
			t.Errorf("%s got %d %v", path, rec.Code, rec.Header())
		}
	}
}
