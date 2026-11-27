package main

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

func response(body io.Reader, size int64, reqHeader, header http.Header) *http.Response {
	req, _ := http.NewRequest("GET", "http://app/", nil)
	req.Header = reqHeader
	h := http.Header{"Content-Type": {"application/json"}, "Etag": {`"v1"`}}
	for k, v := range header {
		h[k] = v
	}
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(body), ContentLength: size, Request: req}
}

func decompress(t *testing.T, enc string, r io.Reader) io.Reader {
	switch enc {
	case "zstd":
		d, err := zstd.NewReader(r)
		if err != nil {
			t.Fatal(err)
		}
		return d
	case "br":
		return brotli.NewReader(r)
	case "gzip":
		d, err := gzip.NewReader(r)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	return r
}

func TestCompress(t *testing.T) {
	big := strings.Repeat(`{"photo":"albums/1"}`, 200)
	for _, c := range []struct {
		accept string
		header http.Header
		size   int64
		want   string
	}{
		{"gzip, deflate, br, zstd", nil, -1, "zstd"},
		{"gzip, br", nil, int64(len(big)), "br"},
		{"gzip, zstd;q=0", nil, -1, "gzip"},
		{"identity", nil, -1, ""},
		{"zstd", nil, 100, ""},
		{"zstd", http.Header{"Content-Type": {"text/event-stream"}}, -1, ""},
		{"zstd", http.Header{"Content-Type": {"image/png"}}, -1, ""},
		{"zstd", http.Header{"Content-Encoding": {"br"}}, -1, "br"},
		{"zstd", http.Header{"Cache-Control": {"public, no-transform"}}, -1, ""},
	} {
		res := response(strings.NewReader(big), c.size, http.Header{"Accept-Encoding": {c.accept}}, c.header)
		compress(res)
		if got := res.Header.Get("Content-Encoding"); got != c.want {
			t.Errorf("%q %v: got encoding %q, want %q", c.accept, c.header, got, c.want)
			continue
		}
		if c.want == "" || c.header != nil {
			continue
		}
		body, err := io.ReadAll(decompress(t, c.want, res.Body))
		if err != nil || string(body) != big {
			t.Errorf("%s: got %d bytes back, %v", c.want, len(body), err)
		}
		if res.Header.Get("Etag") != `W/"v1"` || res.Header.Get("Vary") != "Accept-Encoding" {
			t.Errorf("%s: got headers %v", c.want, res.Header)
		}
	}

	// a range request is left alone so video seeking works
	res := response(strings.NewReader(big), -1, http.Header{"Accept-Encoding": {"zstd"}, "Range": {"bytes=0-"}}, nil)
	compress(res)
	if res.Header.Get("Content-Encoding") != "" {
		t.Error("compressed a range request")
	}
}

func TestCompressStreams(t *testing.T) {
	pr, pw := io.Pipe()
	res := response(pr, -1, http.Header{"Accept-Encoding": {"gzip"}}, nil)
	compress(res)
	var d io.Reader
	for _, chunk := range []string{`{"progress":1}`, `{"progress":2}`} {
		go pw.Write([]byte(chunk))
		if d == nil {
			d = decompress(t, "gzip", res.Body)
		}
		got := make([]byte, len(chunk))
		if _, err := io.ReadFull(d, got); err != nil || !bytes.Equal(got, []byte(chunk)) {
			t.Fatalf("got %q, %v before the app finished", got, err)
		}
	}
	pw.Close()
}
