package docker

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"slices"
)

// allowed is every request tailgate makes, all of them reads
var allowed = []string{"/containers/json", "/networks/bridge", "/events"}

// ProxyHandler forwards tailgate's own requests to upstream and refuses the rest,
// so a compromised tailgate can only read what it already could
func ProxyHandler(upstream http.RoundTripper) http.Handler {
	rp := &httputil.ReverseProxy{
		Transport: upstream,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme, r.Out.URL.Host, r.Out.Host = "http", "docker", "docker"
		},
		// events is a stream
		FlushInterval: -1,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// docker gets the raw path, so an escaped one could route differently than the decoded path checked here
		if r.Method != http.MethodGet || r.URL.RawPath != "" || !slices.Contains(allowed, r.URL.Path) {
			slog.Warn("refused a docker request", "method", r.Method, "path", r.URL.Path)
			http.Error(w, "tailgate's docker proxy only allows the requests tailgate makes", http.StatusForbidden)
			return
		}
		rp.ServeHTTP(w, r)
	})
}

// Proxy serves ProxyHandler on a unix socket at listen until ctx ends. The
// socket is handed to uid, so tailgate can run as that user without the docker socket
func Proxy(ctx context.Context, listen, upstream string, uid int) error {
	if err := os.MkdirAll(filepath.Dir(listen), 0o755); err != nil {
		return err
	}
	// left behind by the last run
	if err := os.Remove(listen); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	ln, err := net.Listen("unix", listen)
	if err != nil {
		return err
	}
	if err := os.Chown(listen, uid, uid); err != nil {
		ln.Close()
		return err
	}
	if err := os.Chmod(listen, 0o600); err != nil {
		ln.Close()
		return err
	}
	srv := &http.Server{Handler: ProxyHandler(UnixTransport(upstream))}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	slog.Info("docker proxy listening", "socket", listen, "docker", upstream)
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
