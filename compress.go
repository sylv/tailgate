package main

import (
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

type encoder interface {
	io.WriteCloser
	Flush() error
	Reset(io.Writer)
}

// encoders in order of preference, at fast settings
var encoders = []struct {
	name string
	pool *sync.Pool
}{
	{"zstd", &sync.Pool{New: func() any {
		// browsers only take windows up to 8MB
		e, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(1<<20))
		return e
	}}},
	{"br", &sync.Pool{New: func() any { return brotli.NewWriterLevel(nil, 1) }}},
	{"gzip", &sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed); return w }}},
}

// compress swaps a proxied response's body for a compressed one when the
// client takes it and compressing helps
func compress(res *http.Response) {
	ct, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if res.StatusCode != http.StatusOK || res.Request.Method == http.MethodHead || res.Request.Header.Get("Range") != "" ||
		res.Header.Get("Content-Encoding") != "" || strings.Contains(res.Header.Get("Cache-Control"), "no-transform") ||
		!compressible(ct) || res.ContentLength >= 0 && res.ContentLength < 1024 {
		return
	}
	res.Header.Add("Vary", "Accept-Encoding")
	accepted := acceptedEncodings(res.Request.Header.Get("Accept-Encoding"))
	for _, e := range encoders {
		if !accepted[e.name] {
			continue
		}
		res.Header.Set("Content-Encoding", e.name)
		res.Header.Del("Content-Length")
		res.Header.Del("Accept-Ranges")
		if etag := res.Header.Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
			res.Header.Set("ETag", "W/"+etag)
		}
		res.ContentLength = -1
		res.Body = encode(res.Body, e.pool)
		return
	}
}

func compressible(ct string) bool {
	switch {
	case ct == "text/event-stream":
		// mcp streams over sse and breaks if it's buffered
		return false
	case strings.HasPrefix(ct, "text/"), strings.HasSuffix(ct, "+json"):
		return true
	}
	switch ct {
	case "application/json", "application/javascript", "application/x-javascript", "image/svg+xml", "application/wasm":
		return true
	}
	return false
}

func acceptedEncodings(header string) map[string]bool {
	accepted := map[string]bool{}
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(part, ";")
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if q, _ := strconv.ParseFloat(v, 64); q == 0 {
				continue
			}
		}
		accepted[strings.ToLower(strings.TrimSpace(name))] = true
	}
	return accepted
}

// encode compresses body as it's read. Every read from the upstream is
// flushed, so streamed responses still arrive as they're sent
func encode(body io.ReadCloser, pool *sync.Pool) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		e := pool.Get().(encoder)
		e.Reset(pw)
		buf := make([]byte, 32<<10)
		var err error
		for err == nil {
			var n int
			n, err = body.Read(buf)
			if n > 0 {
				if _, werr := e.Write(buf[:n]); werr != nil {
					err = werr
				} else if werr := e.Flush(); werr != nil {
					err = werr
				}
			}
		}
		if err == io.EOF {
			err = e.Close()
		}
		e.Reset(nil)
		pool.Put(e)
		pw.CloseWithError(err)
	}()
	return &encoded{pr, body}
}

type encoded struct {
	*io.PipeReader
	body io.ReadCloser
}

func (e *encoded) Close() error {
	e.PipeReader.Close()
	return e.body.Close()
}
