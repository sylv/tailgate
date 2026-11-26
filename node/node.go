package node

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

type State string

const (
	Starting       State = "starting"
	AuthKeyInvalid State = "auth key invalid"
	NeedsSigning   State = "needs signing"
	CertPending    State = "cert pending"
	// WrongName nodes wait for the device to be renamed in the tailscale admin console, Status.Err says to what
	WrongName State = "wrong name"
	// FunnelNotAllowed nodes still serve the tailnet, Status.Err says why funnel failed
	FunnelNotAllowed State = "funnel not allowed"
	Online           State = "online"
	// Failed nodes are stopped, Status.Err says why
	Failed State = "failed"
)

const tag = "tag:tailgate"

type Status struct {
	State State
	// DNSName is the node's full name without the trailing dot, once it has joined
	DNSName     string
	SignCommand string
	Err         error
}

type Node struct {
	name          string
	self          bool
	srv           *tsnet.Server
	cancel        context.CancelFunc
	done          chan struct{}
	registered    chan struct{}
	funnelChanged chan struct{}

	mu     sync.Mutex
	status Status
	funnel bool
}

func (n *Node) Status() Status {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.status
}

// Registered is closed once the node has joined with the right tag
func (n *Node) Registered() <-chan struct{} { return n.registered }

// Done is closed once the node has stopped, including when it failed
func (n *Node) Done() <-chan struct{} { return n.done }

func (n *Node) set(f func(*Status)) {
	n.mu.Lock()
	before := n.status
	f(&n.status)
	after := n.status
	n.mu.Unlock()
	if before.State != after.State || before.SignCommand != after.SignCommand {
		attrs := []any{"node", n.name, "state", after.State}
		if after.DNSName != "" {
			attrs = append(attrs, "dns_name", after.DNSName)
		}
		if after.State == NeedsSigning {
			attrs = append(attrs, "sign_command", after.SignCommand)
		}
		if after.Err != nil {
			attrs = append(attrs, "error", after.Err.Error())
		}
		level := slog.LevelInfo
		if after.State == Failed || after.State == AuthKeyInvalid {
			level = slog.LevelError
		}
		slog.Log(context.Background(), level, "node state", attrs...)
	}
}

// SetFunnel turns the node's funnel listener on or off
func (n *Node) SetFunnel(on bool) {
	n.mu.Lock()
	n.funnel = on
	n.mu.Unlock()
	select {
	case n.funnelChanged <- struct{}{}:
	default:
	}
}

func (n *Node) wantsFunnel() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.funnel
}

func (n *Node) fail(err error) {
	n.set(func(s *Status) { s.State, s.Err = Failed, err })
}

type Manager struct {
	dataDir string
	authKey string
	// one cert request at a time so starting many nodes doesn't hit let's encrypt rate limits
	certSem chan struct{}

	mu    sync.Mutex
	self  *Node
	nodes map[string]*Node
	// removing has the unused nodes being removed, which can't be started meanwhile
	removing map[string]bool
	untagged bool
}

func NewManager(dataDir, authKey string) *Manager {
	return &Manager{dataDir: dataDir, authKey: authKey, certSem: make(chan struct{}, 1), nodes: map[string]*Node{}, removing: map[string]bool{}}
}

func (m *Manager) nodesDir() string { return filepath.Join(m.dataDir, "nodes") }

// StartSelf starts the tailgate node, which keeps whatever name tailscale
// gives it. Funnel connections go to their own handler
func (m *Manager) StartSelf(hostname string, tailnet, funnel http.Handler) *Node {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.self = m.start(hostname, filepath.Join(m.dataDir, "tailgate"), true, tailnet, funnel)
	return m.self
}

// Start starts the node for a service unless it's already running, and turns its funnel on or off
func (m *Manager) Start(name string, h http.Handler, funnel bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.untagged || m.removing[name] {
		return
	}
	if m.nodes[name] == nil {
		m.nodes[name] = m.start(name, filepath.Join(m.nodesDir(), name), false, h, h)
	}
	m.nodes[name].SetFunnel(funnel)
}

// Stop stops a service's node and keeps its state dir
func (m *Manager) Stop(name string) {
	m.mu.Lock()
	n := m.nodes[name]
	delete(m.nodes, name)
	m.mu.Unlock()
	if n != nil {
		n.stop()
		slog.Info("node stopped", "node", name)
	}
}

// Running lists the services that have a node, failed ones too
func (m *Manager) Running() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var names []string
	for name := range m.nodes {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (m *Manager) Self() *Node {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.self
}

func (m *Manager) Get(name string) *Node {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nodes[name]
}

func (m *Manager) Close() {
	m.mu.Lock()
	nodes := m.nodes
	m.nodes = map[string]*Node{}
	if m.self != nil {
		nodes[""] = m.self
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Go(n.stop)
	}
	wg.Wait()
}

func (n *Node) stop() {
	n.cancel()
	<-n.done
}

func (m *Manager) start(name, dir string, self bool, tailnet, funnel http.Handler) *Node {
	ctx, cancel := context.WithCancel(context.Background())
	n := &Node{
		name:          name,
		self:          self,
		cancel:        cancel,
		done:          make(chan struct{}),
		registered:    make(chan struct{}),
		funnelChanged: make(chan struct{}, 1),
		status:        Status{State: Starting},
		srv: &tsnet.Server{
			Dir:      dir,
			Hostname: name,
			AuthKey:  m.authKey,
			UserLogf: func(format string, args ...any) {
				slog.Debug(fmt.Sprintf(format, args...), "node", name)
			},
		},
	}
	slog.Info("node state", "node", name, "state", Starting)
	go m.run(ctx, n, tailnet, funnel)
	return n
}

func (m *Manager) run(ctx context.Context, n *Node, tailnet, funnel http.Handler) {
	defer close(n.done)
	if err := os.MkdirAll(n.srv.Dir, 0o700); err != nil {
		n.fail(err)
		return
	}
	defer n.srv.Close()
	lc, err := n.srv.LocalClient()
	if err != nil {
		n.fail(err)
		return
	}
	if err := m.join(ctx, n, lc); err != nil {
		if ctx.Err() == nil && n.Status().State != AuthKeyInvalid {
			n.fail(err)
		}
		return
	}
	if ctx.Err() != nil {
		return
	}
	// nothing listens until join is done, so a node with the wrong name or no cert never serves
	ln, err := n.srv.Listen("tcp", ":443")
	if err != nil {
		n.fail(err)
		return
	}
	// tsnet's ListenTLS and ListenFunnel leave out NextProtos, which would mean http/1.1 only
	tlsConfig := &tls.Config{NextProtos: []string{"h2", "http/1.1"}, GetCertificate: lc.GetCertificate}
	host := n.Status().DNSName
	hs := server(host, tailnet, func(c net.Conn) *conn {
		return &conn{peer: sync.OnceValues(func() (*Peer, error) { return whois(lc, c.RemoteAddr().String()) })}
	})
	go hs.Serve(tls.NewListener(ln, tlsConfig))
	defer hs.Close()
	if err := n.serve(ctx, lc, tlsConfig, host, funnel); err != nil && ctx.Err() == nil {
		n.fail(err)
	}
}

// serve runs the funnel listener while it's wanted and checks the node every hour
func (n *Node) serve(ctx context.Context, lc *local.Client, tlsConfig *tls.Config, host string, h http.Handler) error {
	// a crash leaves funnel allowed in the node's state, and ListenFunnel never turns off what it didn't turn on
	if sc, err := lc.GetServeConfig(ctx); err != nil {
		return err
	} else if len(sc.AllowFunnel) > 0 {
		if err := lc.SetServeConfig(ctx, &ipn.ServeConfig{}); err != nil {
			return err
		}
	}
	var fs *http.Server
	defer func() {
		if fs != nil {
			fs.Close()
		}
	}()
	var certErr, funnelErr error
	hourly := time.NewTicker(time.Hour)
	defer hourly.Stop()
	retry := time.NewTicker(time.Minute)
	defer retry.Stop()
	for {
		want := n.wantsFunnel()
		if want && fs == nil {
			ln, err := n.srv.ListenFunnel("tcp", ":443", tsnet.FunnelOnly(), tsnet.FunnelTLSConfig(tlsConfig))
			funnelErr = err
			if err == nil {
				fs = server(host, h, funnelConn)
				go fs.Serve(ln)
				slog.Info("funnel on", "node", n.name)
			}
		}
		if !want {
			funnelErr = nil
			if fs != nil {
				fs.Close()
				fs = nil
				slog.Info("funnel off", "node", n.name)
			}
		}
		n.set(func(s *Status) {
			switch {
			case certErr != nil:
				s.State, s.Err = CertPending, certErr
			case funnelErr != nil:
				s.State, s.Err = FunnelNotAllowed, funnelErr
			default:
				s.State, s.Err = Online, nil
			}
		})

		select {
		case <-n.funnelChanged:
		case <-retry.C:
		case <-hourly.C:
			st, err := lc.StatusWithoutPeers(ctx)
			if err != nil {
				return err
			}
			if st.Self.Tags == nil || !slices.Contains(st.Self.Tags.AsSlice(), tag) {
				return fmt.Errorf("this node lost %s, so it was stopped", tag)
			}
			_, _, certErr = lc.CertPair(ctx, n.Status().DNSName)
		case <-ctx.Done():
			return nil
		}
	}
}

type connKey struct{}

// conn is what tailgate knows about a request's connection
type conn struct {
	funnel bool
	// src is the public client address for funnel
	src  string
	peer func() (*Peer, error)
}

// server serves h for host. A request for any other host is refused, since
// the TLS name doesn't bind the Host header and apps build links from it
func server(host string, h http.Handler, info func(net.Conn) *conn) *http.Server {
	return &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if name, _, _ := strings.Cut(r.Host, ":"); !strings.EqualFold(name, host) {
				http.Error(w, "this node only serves "+host, http.StatusMisdirectedRequest)
				return
			}
			c := r.Context().Value(connKey{}).(*conn)
			if c.funnel {
				if c.src == "" {
					http.Error(w, "unknown client address", http.StatusInternalServerError)
					return
				}
				r.RemoteAddr = c.src
			}
			h.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelDebug),
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, connKey{}, info(c))
		},
	}
}

// funnelConn reads the public client address, since RemoteAddr is the funnel relay
func funnelConn(c net.Conn) *conn {
	if tc, ok := c.(*tls.Conn); ok {
		if fc, ok := tc.NetConn().(*ipn.FunnelConn); ok {
			return &conn{funnel: true, src: fc.Src.String()}
		}
	}
	return &conn{funnel: true}
}

// Ingress is "tailnet" or "funnel", from the listener that accepted the
// connection. Anything that isn't known to be the tailnet is funnel
func Ingress(ctx context.Context) string {
	if c, ok := ctx.Value(connKey{}).(*conn); ok && !c.funnel {
		return "tailnet"
	}
	return "funnel"
}

func (m *Manager) join(ctx context.Context, n *Node, lc *local.Client) error {
	st, err := n.srv.Up(ctx)
	if err != nil {
		if ctx.Err() == nil {
			n.set(func(s *Status) { s.State, s.Err = AuthKeyInvalid, err })
		}
		return err
	}

	if st.Self.Tags == nil || !slices.Contains(st.Self.Tags.AsSlice(), tag) {
		m.mu.Lock()
		m.untagged = true
		m.mu.Unlock()
		logoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		logoutErr := lc.Logout(logoutCtx)
		return errors.Join(fmt.Errorf("TS_AUTHKEY isn't tagged %s, so this node was logged out and tailgate won't create any more nodes", tag), logoutErr)
	}

	var dnsName string
	for {
		dnsName = strings.TrimSuffix(st.Self.DNSName, ".")
		got, _, _ := strings.Cut(dnsName, ".")
		n.set(func(s *Status) { s.DNSName, s.Err = dnsName, nil })
		if got == n.name {
			break
		}
		// tailscale renames clashing nodes to name-1 without telling us, and renaming it back
		// in the admin console reaches us in the netmap
		if n.self {
			slog.Warn("tailgate node was renamed by tailscale, probably because another device has the name", "wanted", n.name, "got", got)
			break
		}
		err := fmt.Errorf("tailscale named this node %q, probably because another device is called %q. Free up the name, then rename %q to %q in the tailscale admin console", got, n.name, got, n.name)
		n.set(func(s *Status) { s.State, s.Err = WrongName, err })
		if !sleep(ctx, 5*time.Second) {
			return nil
		}
		if st, err = lc.StatusWithoutPeers(ctx); err != nil {
			return err
		}
	}
	close(n.registered)

	for warned := false; ; {
		lock, err := lc.NetworkLockStatus(ctx)
		if err != nil {
			return err
		}
		if !lock.Enabled || lock.NodeKeySigned {
			break
		}
		if n.self && !warned && !strings.Contains(m.authKey, "--TL") {
			slog.Warn(`tailnet lock is on and TS_AUTHKEY isn't signed, so each new node needs signing. Optional: sign the key once with "tailscale lock sign <key>" on a signing device to skip that`)
			warned = true
		}
		cmd := fmt.Sprintf("tailscale lock sign %v %s", lock.NodeKey, lock.PublicKey.CLIString())
		n.set(func(s *Status) { s.State, s.SignCommand = NeedsSigning, cmd })
		if !sleep(ctx, 5*time.Second) {
			return nil
		}
	}

	n.set(func(s *Status) { s.State = CertPending })
	select {
	case m.certSem <- struct{}{}:
	case <-ctx.Done():
		return nil
	}
	defer func() { <-m.certSem }()
	for wait := time.Second; ; wait = min(wait*2, 5*time.Minute) {
		_, _, err := lc.CertPair(ctx, dnsName)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil
		}
		n.set(func(s *Status) { s.Err = err })
		slog.Warn("fetching cert failed", "node", n.name, "error", err.Error(), "retry_in", wait.String())
		if !sleep(ctx, wait) {
			return nil
		}
	}
	return nil
}

// Peer is the tailnet device a connection came from
type Peer struct {
	// LoginID is the owner's UserProfile.ID, which never changes
	LoginID int64
	Login   string
	Node    string
	Tags    []string
	// Routes is set when the device advertises subnet routes or is an exit node
	Routes bool
}

// Linkable is whether the device may sign its owner in through a linked login.
// tagged devices are servers and a subnet router speaks for its whole lan
func (p *Peer) Linkable() bool { return len(p.Tags) == 0 && !p.Routes }

// PeerOf returns the device a request's connection came from, or nil for
// funnel. WhoIs runs once per connection, since a connection can't change
// which device it comes from
func PeerOf(ctx context.Context) (*Peer, error) {
	c, ok := ctx.Value(connKey{}).(*conn)
	if !ok {
		return nil, errors.New("request didn't come through a node")
	}
	if c.funnel {
		return nil, nil
	}
	return c.peer()
}

func whois(lc *local.Client, addr string) (*Peer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	w, err := lc.WhoIs(ctx, addr)
	if err != nil {
		return nil, err
	}
	var own []netip.Addr
	for _, p := range w.Node.Addresses {
		own = append(own, p.Addr())
	}
	return &Peer{
		LoginID: int64(w.UserProfile.ID),
		Login:   w.UserProfile.LoginName,
		Node:    w.Node.ComputedName,
		Tags:    w.Node.Tags,
		Routes:  len(w.Node.PrimaryRoutes) > 0 || routes(w.Node.AllowedIPs, own),
	}, nil
}

// routes is whether allowed has anything beyond the device's own addresses
func routes(allowed []netip.Prefix, own []netip.Addr) bool {
	return slices.ContainsFunc(allowed, func(p netip.Prefix) bool { return !p.IsSingleIP() || !slices.Contains(own, p.Addr()) })
}

// Peers lists the devices this node can see
func (n *Node) Peers(ctx context.Context) ([]Peer, error) {
	lc, err := n.srv.LocalClient()
	if err != nil {
		return nil, err
	}
	st, err := lc.Status(ctx)
	if err != nil {
		return nil, err
	}
	var peers []Peer
	for _, ps := range st.Peer {
		p := Peer{LoginID: int64(ps.UserID), Login: st.User[ps.UserID].LoginName, Node: strings.TrimSuffix(ps.DNSName, ".")}
		if ps.Tags != nil {
			p.Tags = ps.Tags.AsSlice()
		}
		if ps.PrimaryRoutes != nil && ps.PrimaryRoutes.Len() > 0 {
			p.Routes = true
		}
		if ps.AllowedIPs != nil && routes(ps.AllowedIPs.AsSlice(), ps.TailscaleIPs) {
			p.Routes = true
		}
		peers = append(peers, p)
	}
	return peers, nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// Unused lists the state dirs in nodes/ whose name no container uses
func (m *Manager) Unused(inUse map[string]bool) ([]string, error) {
	entries, err := os.ReadDir(m.nodesDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !inUse[e.Name()] {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// RemoveUnused logs out each unused node and deletes its state dir. Logging
// out leaves the device in the admin console and an auth key can't delete it,
// so it returns the DNS names to delete by hand
func (m *Manager) RemoveUnused(ctx context.Context, inUse map[string]bool) ([]string, error) {
	m.mu.Lock()
	all, err := m.Unused(inUse)
	var names []string
	for _, name := range all {
		if m.nodes[name] == nil && !m.removing[name] {
			m.removing[name] = true
			names = append(names, name)
		}
	}
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	defer func() {
		m.mu.Lock()
		for _, name := range names {
			delete(m.removing, name)
		}
		m.mu.Unlock()
	}()
	var removed []string
	var errs []error
	for _, name := range names {
		dnsName, err := remove(ctx, filepath.Join(m.nodesDir(), name), name)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		slog.Info("removed unused node", "node", name, "dns_name", dnsName)
		removed = append(removed, dnsName)
	}
	return removed, errors.Join(errs...)
}

func remove(ctx context.Context, dir, name string) (string, error) {
	// no auth key, so a dir without usable state can't register a new device
	srv := &tsnet.Server{Dir: dir, Hostname: name, UserLogf: func(string, ...any) {}}
	defer srv.Close()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	st, err := srv.Up(ctx)
	if err != nil {
		return "", err
	}
	lc, err := srv.LocalClient()
	if err != nil {
		return "", err
	}
	if err := lc.Logout(ctx); err != nil {
		return "", err
	}
	srv.Close()
	return strings.TrimSuffix(st.Self.DNSName, "."), os.RemoveAll(dir)
}
