package node

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

type seen struct {
	status        int
	addr, ingress string
	hasPeer       bool
}

func get(t *testing.T, info func(net.Conn) *conn) seen {
	var s seen
	srv := httptest.NewUnstartedServer(nil)
	srv.Config = server("127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := PeerOf(r.Context())
		s.addr, s.ingress, s.hasPeer = r.RemoteAddr, Ingress(r.Context()), p != nil
	}), info)
	srv.Start()
	defer srv.Close()
	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	s.status = res.StatusCode
	return s
}

func TestFunnelConn(t *testing.T) {
	got := get(t, func(net.Conn) *conn { return &conn{funnel: true, src: "203.0.113.9:4444"} })
	if got != (seen{200, "203.0.113.9:4444", "funnel", false}) {
		t.Errorf("funnel got %+v", got)
	}
	// a funnel conn without the client address is refused
	if got := get(t, funnelConn); got.status != 500 || got.ingress != "" {
		t.Errorf("funnel without address got %+v", got)
	}
	got = get(t, func(net.Conn) *conn { return &conn{peer: func() (*Peer, error) { return &Peer{}, nil }} })
	if host, _, _ := net.SplitHostPort(got.addr); host != "127.0.0.1" || got.ingress != "tailnet" || !got.hasPeer {
		t.Errorf("tailnet got %+v", got)
	}
}

func TestWrongHost(t *testing.T) {
	srv := httptest.NewServer(server("photos.your-tailnet.ts.net", http.NotFoundHandler(), func(net.Conn) *conn { return &conn{} }).Handler)
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Host = "evil.example"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("another host got %d", res.StatusCode)
	}
}
