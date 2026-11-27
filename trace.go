package main

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sylv/tailgate/rules"
)

// tracePath is like cloudflare's /cdn-cgi/trace. It only shows what the
// visitor sent or could see anyway, so it's public and runs before the rules
const tracePath = "/.tailgate/trace"

func trace(w http.ResponseWriter, r *http.Request, req rules.Request, id *identity) {
	auth := "none"
	if id != nil {
		auth = id.via
	}
	tlsVersion := ""
	if r.TLS != nil {
		tlsVersion = tls.VersionName(r.TLS.Version)
	}
	var b strings.Builder
	for _, kv := range [][2]string{
		{"host", r.Host},
		{"ip", req.IP},
		{"ts", fmt.Sprintf("%.3f", float64(time.Now().UnixMilli())/1000)},
		{"uag", req.UserAgent},
		{"http", r.Proto},
		{"tls", tlsVersion},
		{"loc", req.Country},
		{"ingress", req.Ingress},
		{"auth", auth},
		{"request_id", requestOf(r.Context()).id},
	} {
		b.WriteString(kv[0] + "=" + kv[1] + "\n")
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(b.String()))
}
