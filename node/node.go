package node

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"maps"
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
	// CertPending nodes are waiting for a cert, Status.Err and RetryAt say why and until when
	CertPending State = "cert pending"
	// LoggedOut service nodes wait for Manager.Login, the tailgate node logs back in itself
	LoggedOut     State = "logged out"
	NeedsApproval State = "needs approval"
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
	RetryAt     time.Time
}

type Node struct {
	name          string
	self          bool
	srv           *tsnet.Server
	cancel        context.CancelFunc
	done          chan struct{}
	ready         chan struct{}
	changed       chan struct{}
	funnelChanged chan struct{}

	mu       sync.Mutex
	status   Status
	funnel   bool
	loginErr error

	// only run's goroutine uses these
	hs, fs     *http.Server
	joined     bool
	loggingIn  bool
	warnedLock bool
	// certFor is the name the cert fields are about
	certFor  string
	certOK   bool
	certErr  error
	certWait time.Duration
	certNext time.Time
}

func (n *Node) Status() Status {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.status
}

// Ready is closed once the node has joined, has its cert and serves the tailnet
func (n *Node) Ready() <-chan struct{} { return n.ready }

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
		msg, level := "node state", slog.LevelInfo
		switch after.State {
		case Failed, AuthKeyInvalid:
			level = slog.LevelError
		case NeedsSigning:
			msg, level = "node is waiting to be signed, run sign_command on a device that can sign", slog.LevelWarn
		case LoggedOut:
			msg, level = "node was logged out, log it in again from the admin page", slog.LevelWarn
		case WrongName, FunnelNotAllowed, NeedsApproval:
			level = slog.LevelWarn
		case CertPending:
			if after.Err != nil {
				level = slog.LevelWarn
			}
		}
		slog.Log(context.Background(), level, msg, attrs...)
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

// to moves the node to st, keeping its DNS name
func (n *Node) to(st Status) {
	n.set(func(s *Status) {
		st.DNSName = s.DNSName
		*s = st
	})
}

func (n *Node) fail(err error) { n.to(Status{State: Failed, Err: err}) }

type Manager struct {
	dataDir string
	authKey string
	// one cert request at a time so starting many nodes doesn't hit let's encrypt rate limits, see cert
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

// StartSelf starts the tailgate node. Funnel connections go to their own handler
func (m *Manager) StartSelf(hostname string, tailnet, funnel http.Handler) *Node {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.self = m.start(hostname, filepath.Join(m.dataDir, "tailgate"), true, tailnet, funnel)
	return m.self
}

// Start starts the node for a service unless it's already running, and turns
// its funnel on or off. Nodes are kept under the service's id, so a new
// hostname restarts the node with the same state dir and device
func (m *Manager) Start(id, hostname string, h http.Handler, funnel bool) {
	m.mu.Lock()
	if n := m.nodes[id]; n != nil && n.name != hostname {
		delete(m.nodes, id)
		m.mu.Unlock()
		n.stop()
		slog.Info("node restarting with a new hostname", "id", id, "from", n.name, "to", hostname)
		m.mu.Lock()
	}
	defer m.mu.Unlock()
	if m.untagged || m.removing[id] {
		return
	}
	if m.nodes[id] == nil {
		m.nodes[id] = m.start(hostname, filepath.Join(m.nodesDir(), id), false, h, h)
	}
	m.nodes[id].SetFunnel(funnel)
}

// Stop stops a service's node and keeps its state dir
func (m *Manager) Stop(id string) {
	m.mu.Lock()
	n := m.nodes[id]
	delete(m.nodes, id)
	m.mu.Unlock()
	if n != nil {
		n.stop()
		slog.Info("node stopped", "node", n.name)
	}
}

// Running lists the ids of the services that have a node, failed ones too
func (m *Manager) Running() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Sorted(maps.Keys(m.nodes))
}

func (m *Manager) Self() *Node {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.self
}

// Get returns the node of the service with this id
func (m *Manager) Get(id string) *Node {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nodes[id]
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
		ready:         make(chan struct{}),
		changed:       make(chan struct{}, 1),
		funnelChanged: make(chan struct{}, 1),
		status:        Status{State: Starting},
		srv: &tsnet.Server{
			Dir:      dir,
			Hostname: name,
			UserLogf: func(format string, args ...any) {
				slog.Debug(fmt.Sprintf(format, args...), "node", name)
			},
			// tailscale's own logs are chatty, so they're formatted only when debug logs are on
			Logf: func(format string, args ...any) {
				if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
					slog.Debug(fmt.Sprintf(format, args...), "node", name)
				}
			},
		},
	}
	// tailscale sends the auth key with every login, so a removed node could come back on
	// its own. only new nodes and the tailgate node get it, a logged out service node waits for Login
	if _, err := os.Stat(filepath.Join(dir, "tailscaled.state")); self || errors.Is(err, os.ErrNotExist) {
		n.srv.AuthKey = m.authKey
	}
	slog.Info("node state", "node", name, "state", Starting)
	go m.run(ctx, n, tailnet, funnel)
	return n
}

// run checks the node again whenever tailscale says something changed, funnel is
// turned on or off or a cert is due, until ctx ends or the node fails
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
	defer n.stopServing()
	// the watch ends with run, including when the node fails
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go n.watch(ctx, lc)
	for {
		wait, err := m.check(ctx, n, lc, tailnet, funnel)
		if err != nil {
			if ctx.Err() == nil && n.Status().State != AuthKeyInvalid {
				n.fail(err)
			}
			return
		}
		var due <-chan time.Time
		if wait > 0 {
			due = time.After(wait)
		}
		select {
		case <-n.changed:
		case <-n.funnelChanged:
		case <-due:
		case <-ctx.Done():
			return
		}
	}
}

func (n *Node) poke() {
	select {
	case n.changed <- struct{}{}:
	default:
	}
}

// watch pokes the node whenever tailscale's state or the node's own netmap entry
// changes, which covers its login, tags, signature, name and https and funnel access
func (n *Node) watch(ctx context.Context, lc *local.Client) {
	for ctx.Err() == nil {
		if w, err := lc.WatchIPNBus(ctx, ipn.NotifyInitialState); err == nil {
			for {
				note, err := w.Next()
				if err != nil {
					break
				}
				if note.ErrMessage != nil {
					n.mu.Lock()
					n.loginErr = errors.New(*note.ErrMessage)
					n.mu.Unlock()
				}
				if note.State != nil || note.SelfChange != nil || note.ErrMessage != nil {
					n.poke()
				}
			}
			w.Close()
		}
		// tailscale closes a watch that falls behind, so look again for whatever was missed
		n.poke()
		sleep(ctx, time.Second)
	}
}

// check gets the node as close to serving as it can and stops serving when
// anything is wrong. It returns how long until the cert needs another look
func (m *Manager) check(ctx context.Context, n *Node, lc *local.Client, tailnet, funnel http.Handler) (time.Duration, error) {
	n.mu.Lock()
	loginErr := n.loginErr
	n.mu.Unlock()
	if loginErr != nil {
		n.to(Status{State: AuthKeyInvalid, Err: loginErr})
		return 0, loginErr
	}
	st, err := lc.StatusWithoutPeers(ctx)
	if err != nil {
		return 0, err
	}

	if st.BackendState != ipn.Running.String() {
		n.stopServing()
		switch {
		case st.BackendState == ipn.NeedsMachineAuth.String():
			n.to(Status{State: NeedsApproval, Err: errors.New("device approval is on for this tailnet, approve this device in the tailscale admin console")})
		case st.BackendState == ipn.NeedsLogin.String() && n.self && n.joined && !n.loggingIn:
			// tailgate can't work without its own node, so it logs straight back in
			n.loggingIn = true
			return 0, n.login(ctx, lc, m.authKey)
		case st.BackendState == ipn.NeedsLogin.String() && (n.joined || n.srv.AuthKey == ""):
			n.to(Status{State: LoggedOut, Err: errors.New("this node was logged out of the tailnet, probably because it was removed or its key expired")})
		default:
			n.to(Status{State: Starting})
		}
		return 0, nil
	}
	n.loggingIn = false

	if st.Self.Tags == nil || !slices.Contains(st.Self.Tags.AsSlice(), tag) {
		n.stopServing()
		if n.joined {
			return 0, fmt.Errorf("this node lost %s, so it was stopped", tag)
		}
		m.mu.Lock()
		m.untagged = true
		m.mu.Unlock()
		logoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		logoutErr := lc.Logout(logoutCtx)
		return 0, errors.Join(fmt.Errorf("TS_AUTHKEY isn't tagged %s, so this node was logged out and tailgate won't create any more nodes", tag), logoutErr)
	}
	if !n.joined {
		n.joined = true
		// a crash leaves funnel allowed in the node's state, and ListenFunnel never turns off what it didn't turn on
		if err := lc.SetServeConfig(ctx, &ipn.ServeConfig{}); err != nil {
			return 0, err
		}
	}

	lock, err := lc.NetworkLockStatus(ctx)
	if err != nil {
		return 0, err
	}
	if lock.Enabled && !lock.NodeKeySigned {
		n.stopServing()
		if n.self && !n.warnedLock && !strings.Contains(m.authKey, "--TL") {
			slog.Warn(`tailnet lock is on and TS_AUTHKEY isn't signed, so each new node needs signing. Optional: sign the key once with "tailscale lock sign <key>" on a signing device to skip that`)
			n.warnedLock = true
		}
		n.to(Status{State: NeedsSigning, SignCommand: fmt.Sprintf("tailscale lock sign %v %s", lock.NodeKey, lock.PublicKey.CLIString())})
		return 0, nil
	}

	// tailscale renames clashing nodes to name-1 without telling us, and renaming it back
	// in the admin console reaches us as a self change
	dnsName := strings.TrimSuffix(st.Self.DNSName, ".")
	n.set(func(s *Status) { s.DNSName = dnsName })
	if got, _, _ := strings.Cut(dnsName, "."); got != n.name {
		n.stopServing()
		err := fmt.Errorf("tailscale named this node %q, probably because another device is called %q. Free up the name, then rename %q to %q in the tailscale admin console", got, n.name, got, n.name)
		if n.self {
			err = fmt.Errorf("%w, or set TAILGATE_HOSTNAME to %q", err, got)
		}
		n.to(Status{State: WrongName, Err: err})
		return 0, nil
	}
	if len(st.CertDomains) == 0 {
		n.stopServing()
		n.to(Status{State: CertPending, Err: errors.New("HTTPS certificates are off for this tailnet. Turn them on in the DNS page of the tailscale admin console")})
		return 0, nil
	}

	if n.certFor != dnsName {
		// the old name's listeners go now, the new name needs its own cert
		n.stopServing()
		n.certFor, n.certOK, n.certErr, n.certWait, n.certNext = dnsName, false, nil, 0, time.Time{}
	}
	if !time.Now().Before(n.certNext) {
		if !n.certOK {
			n.to(Status{State: CertPending, Err: n.certErr})
		}
		err := m.cert(ctx, lc, dnsName)
		if ctx.Err() != nil {
			return 0, nil
		}
		if err != nil {
			n.certErr, n.certWait = err, min(max(2*n.certWait, time.Minute), time.Hour)
			slog.Warn("fetching cert failed", "node", n.name, "error", err.Error(), "retry_in", n.certWait.String())
		} else {
			n.certOK, n.certErr, n.certWait = true, nil, 0
		}
		// a cert that works is checked hourly, tsnet renews it when it's close to expiring
		n.certNext = time.Now().Add(cmp.Or(n.certWait, time.Hour))
	}
	if !n.certOK {
		n.to(Status{State: CertPending, Err: n.certErr, RetryAt: n.certNext})
		return time.Until(n.certNext), nil
	}

	if n.hs == nil {
		ln, err := n.srv.Listen("tcp", ":443")
		if err != nil {
			return 0, err
		}
		n.hs = server(dnsName, tailnet, func(c net.Conn) *conn {
			return &conn{peer: sync.OnceValues(func() (*Peer, error) { return whois(lc, c.RemoteAddr().String()) })}
		})
		go n.hs.Serve(tls.NewListener(ln, n.tlsConfig(lc)))
	}
	var funnelErr error
	switch want := n.wantsFunnel(); {
	case want && n.fs == nil:
		ln, err := n.srv.ListenFunnel("tcp", ":443", tsnet.FunnelOnly(), tsnet.FunnelTLSConfig(n.tlsConfig(lc)))
		if funnelErr = err; err == nil {
			n.fs = server(dnsName, funnel, funnelConn)
			go n.fs.Serve(ln)
			slog.Info("funnel on", "node", n.name)
		}
	case !want && n.fs != nil:
		n.fs.Close()
		n.fs = nil
		slog.Info("funnel off", "node", n.name)
	}
	switch {
	case n.certErr != nil:
		n.to(Status{State: CertPending, Err: n.certErr, RetryAt: n.certNext})
	case funnelErr != nil:
		n.to(Status{State: FunnelNotAllowed, Err: funnelErr})
	default:
		n.to(Status{State: Online})
	}
	select {
	case <-n.ready:
	default:
		close(n.ready)
	}
	return time.Until(n.certNext), nil
}

// tsnet's ListenTLS and ListenFunnel leave out NextProtos, which would mean http/1.1 only
func (n *Node) tlsConfig(lc *local.Client) *tls.Config {
	return &tls.Config{NextProtos: []string{"h2", "http/1.1"}, GetCertificate: lc.GetCertificate}
}

func (n *Node) stopServing() {
	if n.hs != nil {
		n.hs.Close()
		n.hs = nil
	}
	if n.fs != nil {
		n.fs.Close()
		n.fs = nil
		slog.Info("funnel off", "node", n.name)
	}
}

// cert gets a cert for name, one node at a time. After a failure the next node
// waits a bit, so nodes retrying together don't all reach let's encrypt at once
func (m *Manager) cert(ctx context.Context, lc *local.Client, name string) error {
	select {
	case m.certSem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	// tailscale waits on let's encrypt with no deadline, so one stuck order would hold up every other node
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	_, _, err := lc.CertPair(ctx, name)
	if err != nil {
		time.AfterFunc(10*time.Second, func() { <-m.certSem })
	} else {
		<-m.certSem
	}
	return err
}

// login logs the node in with the auth key. Its state dir keeps its machine key,
// so it comes back as the same device if that still exists
func (n *Node) login(ctx context.Context, lc *local.Client, authKey string) error {
	n.mu.Lock()
	n.loginErr = nil
	n.mu.Unlock()
	if err := lc.Start(ctx, ipn.Options{AuthKey: authKey}); err != nil {
		return err
	}
	return lc.StartLoginInteractive(ctx)
}

// Login logs a logged out service node back in
func (m *Manager) Login(ctx context.Context, id string) error {
	n := m.Get(id)
	if n == nil || n.Status().State != LoggedOut {
		return errors.New("that node isn't logged out")
	}
	lc, err := n.srv.LocalClient()
	if err != nil {
		return err
	}
	return n.login(ctx, lc, m.authKey)
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
				http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
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
