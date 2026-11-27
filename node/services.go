package node

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
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
	// advertised is when the node started advertising the service
	advertised time.Time
	// lns listen on ips, the service's ips. tailscale gives them to the node once it's approved
	lns  []net.Listener
	ips  []netip.Addr
	hs   *http.Server
	cert certState
	// fetching is set while a cert is being fetched in the background, which sends the result on certDone
	fetching bool
	certDone chan error
}

func (h *hosted) close() {
	for _, ln := range h.lns {
		ln.Close()
	}
	if h.hs != nil {
		go shutdown(h.hs)
	}
	h.hs, h.lns, h.ips = nil, nil, nil
}

// Host serves an app as a tailscale service on the tailgate node, making the
// service in tailscale first. Like a node it's kept under the app's id, so a
// new hostname makes a new tailscale service
func (m *Manager) Host(id, hostname string, handler http.Handler) {
	self := m.Self()
	// tailscale won't make a service while a device has its name, and a stopped node's device stays
	_, blocked := m.device(id)
	self.mu.Lock()
	cur := self.want[id]
	self.mu.Unlock()
	if cur != nil && cur.name == hostname && cur.blocked == blocked {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &hosted{box: box{key: "service", name: hostname, changed: m.changed}, id: id, h: handler, blocked: blocked, defined: make(chan struct{}), cancel: cancel, certDone: make(chan error, 1)}
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

// Unhost stops serving an app's tailscale service and keeps it in tailscale, see DeleteServices
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

// DeleteServices deletes the tailscale services tailgate made that it doesn't serve,
// except the ones named in keep, by hostname. Services without tag:tailgate aren't tailgate's and are left alone
func (m *Manager) DeleteServices(ctx context.Context, keep map[string]bool) {
	self := m.Self()
	self.mu.Lock()
	for _, h := range self.want {
		keep[h.name] = true
	}
	self.mu.Unlock()
	for _, name := range m.api.defined() {
		if !keep[strings.TrimPrefix(name, "svc:")] {
			if err := m.api.undefine(ctx, name); err != nil {
				slog.Error("deleting an unused tailscale service failed", "service", name, "error", err.Error())
			}
		}
	}
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
	next := time.Hour
	var names []tailcfg.ServiceName
	for id, h := range want {
		select {
		case <-h.defined:
			n.serving[id] = h
			names = append(names, "svc:"+tailcfg.ServiceName(h.name))
		default:
		}
	}
	if err := advertise(ctx, lc, names); err != nil {
		slog.Warn("advertising tailscale services failed", "error", err.Error())
		next = 5 * time.Second
	}
	// tailscale tells a host which services it's approved for, and their ips, in its own capabilities
	addrs := map[tailcfg.ServiceName][]netip.Addr{}
	caps, _ := tailcfg.UnmarshalNodeCapJSON[tailcfg.ServiceIPMappings](st.Self.CapMap, tailcfg.NodeAttrServiceHost)
	for _, mapping := range caps {
		maps.Copy(addrs, mapping)
	}
	for _, h := range n.serving {
		if wait := m.serveOne(ctx, n, lc, st, h, addrs["svc:"+tailcfg.ServiceName(h.name)]); wait > 0 {
			next = min(next, wait)
		}
	}
	return next
}

// advertise makes the node host exactly these tailscale services. an empty handler for 443 in the
// serve config is what makes netstack take a service's connections, and with no forward
// address it hands them to whatever srv.Listen has the service's ip. ListenService would forward
// them to a localhost socket instead, which anything sharing tailgate's network could reach
func advertise(ctx context.Context, lc *local.Client, names []tailcfg.ServiceName) error {
	slices.Sort(names)
	sc, err := lc.GetServeConfig(ctx)
	if err != nil {
		return err
	}
	if sc == nil {
		sc = new(ipn.ServeConfig)
	}
	svcs := map[tailcfg.ServiceName]*ipn.ServiceConfig{}
	for _, name := range names {
		svcs[name] = &ipn.ServiceConfig{TCP: map[uint16]*ipn.TCPPortHandler{443: {}}}
	}
	if len(sc.Services)+len(svcs) > 0 && !reflect.DeepEqual(sc.Services, svcs) {
		sc.Services = svcs
		if err := lc.SetServeConfig(ctx, sc); err != nil {
			return err
		}
	}
	prefs, err := lc.GetPrefs(ctx)
	if err != nil {
		return err
	}
	str := make([]string, len(names))
	for i, name := range names {
		str[i] = name.String()
	}
	if !slices.Equal(prefs.AdvertiseServices, str) {
		_, err = lc.EditPrefs(ctx, &ipn.MaskedPrefs{AdvertiseServicesSet: true, Prefs: ipn.Prefs{AdvertiseServices: str}})
	}
	return err
}

func (m *Manager) serveOne(ctx context.Context, n *Node, lc *local.Client, st *ipnstate.Status, h *hosted, addrs []netip.Addr) time.Duration {
	fqdn := h.name + "." + st.CurrentTailnet.MagicDNSSuffix
	h.set(func(s *Status) { s.DNSName = fqdn })
	if h.hs != nil && !slices.Equal(h.ips, addrs) {
		h.close()
	}
	if h.advertised.IsZero() {
		h.advertised = time.Now()
	}
	// approval arrives as a self change. tailscale doesn't say whether one is on its way, and
	// tsnet stops advertising every service on start, so even auto approved services wait a
	// few seconds after every restart. it only needs someone once it's slower than that
	if len(addrs) == 0 {
		if wait := time.Until(h.advertised.Add(20 * time.Second)); wait > 0 {
			h.to(Status{State: Starting})
			return wait
		}
		h.to(Status{State: NeedsApproval, Err: fmt.Errorf("approve tailgate as a host of svc:%s in the services page of the tailscale admin console, or auto approve services tagged %s for %s in your policy file", h.name, tag, tag)})
		return 0
	}
	if h.cert.name != fqdn {
		h.cert = certState{name: fqdn}
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
			h.certDone <- m.cert(ctx, lc, c.name)
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
		for _, addr := range addrs {
			ln, err := n.srv.Listen("tcp", netip.AddrPortFrom(addr, 443).String())
			if err != nil {
				h.close()
				h.fail(fmt.Errorf("listening on the tailscale service's ip failed: %w", err))
				return time.Minute
			}
			h.lns = append(h.lns, ln)
		}
		h.ips = addrs
		h.hs = server(fqdn, h.h, tailnetConn(lc))
		for _, ln := range h.lns {
			go h.hs.Serve(tls.NewListener(ln, n.tlsConfig(lc)))
		}
		slog.Debug("serving tailscale service", "service", h.name)
	}
	if h.cert.err != nil {
		h.to(Status{State: CertPending, Err: h.cert.err, RetryAt: h.cert.next})
	} else {
		h.to(Status{State: Online})
	}
	return time.Until(h.cert.next)
}
