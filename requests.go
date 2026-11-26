package main

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/http"
	"time"

	"github.com/sylv/tailgate/node"
	"github.com/sylv/tailgate/rules"
)

const requestIDHeader = "X-Tailgate-Request-Id"

type request struct {
	id   string
	user string
	via  string
	// rules has the header effects to apply when the request is proxied
	rules *rules.Result
	// rule is the rule that blocked or redirected the request
	rule    string
	ruleErr string
}

type requestKey struct{}

func requestOf(ctx context.Context) *request {
	if r, ok := ctx.Value(requestKey{}).(*request); ok {
		return r
	}
	return &request{rules: &rules.Result{}}
}

// requests gives every request an ID and logs one line for it when it's done
func requests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := &request{id: rand.Text(), rules: &rules.Result{}}
		w.Header().Set(requestIDHeader, req.id)
		rec := &recorder{ResponseWriter: w}
		start := time.Now()
		h.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), requestKey{}, req)))
		attrs := []any{"request_id", req.id, "ingress", node.Ingress(r.Context()), "host", r.Host, "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration_ms", time.Since(start).Milliseconds()}
		if req.user != "" {
			attrs = append(attrs, "user", req.user, "via", req.via)
		}
		if req.rule != "" {
			attrs = append(attrs, "rule", req.rule)
		}
		if req.ruleErr != "" {
			attrs = append(attrs, "rule_error", req.ruleErr)
		}
		if len(req.rules.Logged) > 0 {
			attrs = append(attrs, "log_only", req.rules.Logged)
		}
		slog.Info("request", attrs...)
	})
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the connection, which the proxy needs for websockets and flushing
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// plain is every answer tailgate gives itself on a service host
func plain(w http.ResponseWriter, r *http.Request, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write([]byte(msg + "\n\nrequest id: " + requestOf(r.Context()).id + "\n"))
}

func redirect(w http.ResponseWriter, to string) {
	// not http.Redirect, which cleans the path and would change an app's urls
	w.Header().Set("Location", to)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusFound)
}
