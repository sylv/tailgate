package node

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/pires/go-proxyproto"
	"tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
)

// hosted is an app the tailgate node serves as a tailscale service
// instead of giving it a node
type hosted struct {
	box
	id string
	h  http.Handler
	// blocked is set while the app's old node dir holds the name
	blocked bool
	// defined is closed once the tailscale service exists
	defined chan struct{}
	cancel  context.CancelFunc

	// only the tailgate node's run goroutine uses these
	ln   *tsnet.ServiceListener
	hs   *http.Server
	cert certState
	// fetching is set while a cert is being fetched in the background, which sends the result on certDone
	fetching bool
	certDone chan error
}

func (h *hosted) close() {
	// closing the server closes the listener too, which stops advertising the service
	if h.hs != nil {
		h.hs.Close()
	} else if h.ln != nil {
		h.ln.Close()
	}
	h.hs, h.ln = nil, nil
}

// Host serves an app as a tailscale service on the tailgate node, making the
// service in tailscale first. Like a node it's kept under the app's id, so a
// new hostname makes a new tailscale service
func (m *Manager) Host(id, hostname string, handler http.Handler) {
	self := m.Self()
	// tailscale won't make a service while a device has its name, and a stopped node's device stays
	_, err := os.Stat(filepath.Join(m.nodesDir(), id))
	blocked := !errors.Is(err, os.ErrNotExist)
	self.mu.Lock()
	cur := self.want[id]
	self.mu.Unlock()
	if cur != nil && cur.name == hostname && cur.blocked == blocked {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &hosted{box: box{key: "service", name: hostname}, id: id, h: handler, blocked: blocked, defined: make(chan struct{}), cancel: cancel, certDone: make(chan error, 1)}
	if blocked {
		h.to(Status{State: WrongName, Err: errors.New("the device of the node this app used before still has its name. Delete it in the tailscale admin console and tailgate carries on")})
	} else {
		h.to(Status{State: Starting})
		go func() {
			if retry(ctx, &h.box, func() error { return m.api.define(ctx, "svc:"+hostname) }) == nil {
				close(h.defined)
				self.poke()
			}
		}()
	}
	self.mu.Lock()
	self.want[id] = h
	self.mu.Unlock()
	if cur != nil {
		cur.cancel()
	}
	self.poke()
}

// Unhost stops serving an app's tailscale service and keeps it in tailscale
func (m *Manager) Unhost(id string) {
	self := m.Self()
	self.mu.Lock()
	if h := self.want[id]; h != nil {
		h.cancel()
	}
	delete(self.want, id)
	self.mu.Unlock()
	self.poke()
}

// Hosted lists the ids of the apps the tailgate node serves as tailscale services
func (m *Manager) Hosted() []string {
	self := m.Self()
	self.mu.Lock()
	defer self.mu.Unlock()
	return slices.Sorted(maps.Keys(self.want))
}

func (m *Manager) hostedNames() map[string]bool {
	self := m.Self()
	names := map[string]bool{}
	if self == nil {
		return names
	}
	self.mu.Lock()
	defer self.mu.Unlock()
	for _, h := range self.want {
		names["svc:"+h.name] = true
	}
	return names
}

// Status is the status of an app's node or tailscale service
func (m *Manager) Status(id string) (Status, bool) {
	if n := m.Get(id); n != nil {
		return n.Status(), true
	}
	if self := m.Self(); self != nil {
		self.mu.Lock()
		h := self.want[id]
		self.mu.Unlock()
		if h != nil {
			return h.Status(), true
		}
	}
	return Status{}, false
}

// serveHosted serves each tailscale service once tailscale has approved the
// tailgate node to host it. It returns how long until a cert needs another look
func (m *Manager) serveHosted(ctx context.Context, n *Node, lc *local.Client, st *ipnstate.Status) time.Duration {
	n.mu.Lock()
	want := maps.Clone(n.want)
	n.mu.Unlock()
	for id, h := range n.serving {
		if want[id] != h {
			h.close()
			delete(n.serving, id)
		}
	}
	// tailscale tells a host which services it's approved for in its own capabilities
	approved := map[tailcfg.ServiceName]bool{}
	caps, _ := tailcfg.UnmarshalNodeCapJSON[tailcfg.ServiceIPMappings](st.Self.CapMap, tailcfg.NodeAttrServiceHost)
	for _, mapping := range caps {
		for name := range mapping {
			approved[name] = true
		}
	}
	next := time.Hour
	for id, h := range want {
		select {
		case <-h.defined:
		default:
			continue
		}
		n.serving[id] = h
		if wait := m.serveOne(ctx, n, lc, st, h, approved["svc:"+tailcfg.ServiceName(h.name)]); wait > 0 {
			next = min(next, wait)
		}
	}
	return next
}

func (m *Manager) serveOne(ctx context.Context, n *Node, lc *local.Client, st *ipnstate.Status, h *hosted, approved bool) time.Duration {
	fqdn := h.name + "." + st.CurrentTailnet.MagicDNSSuffix
	h.set(func(s *Status) { s.DNSName = fqdn })
	if h.hs != nil && !approved {
		h.close()
	}
	if h.ln == nil {
		// tailscale hands connections to a localhost socket with a PROXY header saying who
		// they're from, so anything else in tailgate's network namespace could claim any
		// address. that's why tailgate can't share the host's network
		ln, err := n.srv.ListenService("svc:"+h.name, tsnet.ServiceModeTCP{Port: 443, PROXYProtocolVersion: 2})
		if err != nil {
			h.fail(fmt.Errorf("advertising the tailscale service failed: %w", err))
			return time.Minute
		}
		h.ln = ln
	}
	// approval arrives as a self change, even an auto approved one takes a few seconds
	if !approved {
		h.to(Status{State: NeedsApproval, Err: fmt.Errorf("approve tailgate as a host of svc:%s in the services page of the tailscale admin console, or auto approve services tagged %s for %s in your policy file", h.name, tag, tag)})
		return 0
	}
	if h.cert.name != fqdn {
		h.cert = certState{name: fqdn, dir: n.srv.Dir}
	}
	// every service waits its turn for a cert, so it's fetched in the background to keep the
	// tailgate node's own checks and the other services' statuses going meanwhile
	select {
	case err := <-h.certDone:
		h.fetching = false
		if ctx.Err() == nil {
			h.cert.record(err, &h.box)
		}
	default:
	}
	if !h.fetching && !time.Now().Before(h.cert.next) {
		h.fetching = true
		go func(c certState) {
			h.certDone <- m.cert(ctx, lc, c.dir, c.name)
			n.poke()
		}(h.cert)
	}
	if !h.cert.ok {
		st := Status{State: CertPending, Err: h.cert.err}
		if !h.fetching {
			st.RetryAt = h.cert.next
		}
		h.to(st)
		if h.fetching {
			return 0
		}
		return time.Until(h.cert.next)
	}
	if h.hs == nil {
		h.hs = server(fqdn, h.h, tailnetConn(lc))
		pl := &proxyproto.Listener{
			Listener:   h.ln,
			ConnPolicy: func(proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) { return proxyproto.REQUIRE, nil },
		}
		go h.hs.Serve(tls.NewListener(pl, n.tlsConfig(lc)))
		slog.Debug("serving tailscale service", "service", h.name)
	}
	if h.cert.err != nil {
		h.to(Status{State: CertPending, Err: h.cert.err, RetryAt: h.cert.next})
	} else {
		h.to(Status{State: Online})
	}
	return time.Until(h.cert.next)
}
