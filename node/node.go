package node

import (
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
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
	// LoggedOut app nodes wait for Manager.Login, the tailgate node logs back in itself
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
	// Service is set when the tailgate node serves it as a tailscale service
	Service bool
}

// box holds a node's or a tailscale service's status and logs its changes
type box struct {
	// key is "node" or "service", for the logs
	key, name string

	smu    sync.Mutex
	status Status
}

func (b *box) Status() Status {
	b.smu.Lock()
	defer b.smu.Unlock()
	return b.status
}

func (b *box) set(f func(*Status)) {
	b.smu.Lock()
	before := b.status
	f(&b.status)
	b.status.Service = b.key == "service"
	after := b.status
	b.smu.Unlock()
	if before.State != after.State || before.SignCommand != after.SignCommand {
		attrs := []any{b.key, b.name, "state", after.State}
		if after.DNSName != "" {
			attrs = append(attrs, "dns_name", after.DNSName)
		}
		if after.State == NeedsSigning {
			attrs = append(attrs, "sign_command", after.SignCommand)
		}
		if after.Err != nil {
			attrs = append(attrs, "error", after.Err.Error())
		}
		msg, level := b.key+" state", slog.LevelInfo
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

// to moves to st, keeping the DNS name
func (b *box) to(st Status) {
	b.set(func(s *Status) {
		st.DNSName = s.DNSName
		*s = st
	})
}

func (b *box) fail(err error) { b.to(Status{State: Failed, Err: err}) }

// certState is how getting the cert for name is going. dir is the node's state dir, which caches it
type certState struct {
	name string
	dir  string
	ok   bool
	err  error
	wait time.Duration
	next time.Time
}

type Node struct {
	box
	self          bool
	srv           *tsnet.Server
	cancel        context.CancelFunc
	done          chan struct{}
	ready         chan struct{}
	changed       chan struct{}
	funnelChanged chan struct{}

	mu       sync.Mutex
	funnel   bool
	loginErr error
	// want is the tailscale services the tailgate node should serve, by app id
	want map[string]*hosted

	// only run's goroutine uses these
	hs, fs    *http.Server
	joined    bool
	loggingIn bool
	cert      certState
	serving   map[string]*hosted
	// fresh nodes have no state yet, so they join with a new auth key
	fresh bool
	// shuttingDown keeps the services' statuses as they were, they're only reset for a reconnect
	shuttingDown bool
}

// Ready is closed once the node has joined under its own name
func (n *Node) Ready() <-chan struct{} { return n.ready }

// Done is closed once the node has stopped, including when it failed
func (n *Node) Done() <-chan struct{} { return n.done }

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

type Manager struct {
	dataDir string
	api     *API
	// one cert request at a time so starting many nodes doesn't hit let's encrypt rate limits, see cert
	certSem chan struct{}

	mu       sync.Mutex
	self     *Node
	nodes    map[string]*Node
	untagged bool
}

func NewManager(dataDir string, api *API) *Manager {
	return &Manager{dataDir: dataDir, api: api, certSem: make(chan struct{}, 1), nodes: map[string]*Node{}}
}

func (m *Manager) nodesDir() string { return filepath.Join(m.dataDir, "nodes") }

// StartSelf starts the tailgate node. Funnel connections go to their own handler
func (m *Manager) StartSelf(hostname string, tailnet, funnel http.Handler) *Node {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.self = m.start(hostname, filepath.Join(m.dataDir, "tailgate"), true, tailnet, funnel)
	return m.self
}

// Start starts the node for an app unless it's already running, and turns
// its funnel on or off. Nodes are kept under the app's id, so a new
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
	if m.untagged {
		return
	}
	if m.nodes[id] == nil {
		m.nodes[id] = m.start(hostname, filepath.Join(m.nodesDir(), id), false, h, h)
	}
	m.nodes[id].SetFunnel(funnel)
}

// Stop stops an app's node and keeps its state dir
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

// Running lists the ids of the apps that have a node, failed ones too
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

// Get returns the node of the app with this id
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
		box:           box{key: "node", name: name, status: Status{State: Starting}},
		self:          self,
		cancel:        cancel,
		done:          make(chan struct{}),
		ready:         make(chan struct{}),
		changed:       make(chan struct{}, 1),
		funnelChanged: make(chan struct{}, 1),
		want:          map[string]*hosted{},
		serving:       map[string]*hosted{},
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
	// its own. only new nodes get one, a logged out app node waits for Login
	_, err := os.Stat(filepath.Join(dir, "tailscaled.state"))
	n.fresh = errors.Is(err, os.ErrNotExist)
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
	// a tailscale service tailgate made with the node's name holds the name
	if !n.self && retry(ctx, &n.box, func() error { return m.api.undefine(ctx, "svc:"+n.name) }) != nil {
		return
	}
	if n.fresh && retry(ctx, &n.box, func() (err error) { n.srv.AuthKey, err = m.api.authKey(ctx); return err }) != nil {
		return
	}
	defer n.srv.Close()
	lc, err := n.srv.LocalClient()
	if err != nil {
		n.fail(err)
		return
	}
	defer func() {
		n.shuttingDown = true
		n.stopServing()
	}()
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
		case st.BackendState == ipn.NeedsLogin.String() && n.self && (n.joined || !n.fresh) && !n.loggingIn:
			// tailgate can't work without its own node, so it logs straight back in
			n.loggingIn = true
			var key string
			if err := retry(ctx, &n.box, func() (err error) { key, err = m.api.authKey(ctx); return err }); err != nil {
				return 0, err
			}
			return 0, m.login(ctx, n, lc, key)
		case st.BackendState == ipn.NeedsLogin.String() && (n.joined || !n.fresh):
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
		return 0, errors.Join(fmt.Errorf("this node isn't tagged %s, so it was logged out and tailgate won't create any more nodes", tag), logoutErr)
	}
	if !n.joined {
		n.joined = true
		// a crash leaves funnel allowed and services advertised in the node's state, and
		// ListenFunnel and ListenService never turn off what they didn't turn on
		if err := lc.SetServeConfig(ctx, &ipn.ServeConfig{}); err != nil {
			return 0, err
		}
		if _, err := lc.EditPrefs(ctx, &ipn.MaskedPrefs{AdvertiseServicesSet: true}); err != nil {
			return 0, err
		}
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
	select {
	case <-n.ready:
	default:
		close(n.ready)
	}

	lock, err := lc.NetworkLockStatus(ctx)
	if err != nil {
		return 0, err
	}
	if lock.Enabled && !lock.NodeKeySigned {
		n.stopServing()
		n.to(Status{State: NeedsSigning, SignCommand: fmt.Sprintf("tailscale lock sign %v %s", lock.NodeKey, lock.PublicKey.CLIString())})
		return 0, nil
	}
	if len(st.CertDomains) == 0 {
		n.stopServing()
		n.to(Status{State: CertPending, Err: errors.New("HTTPS certificates are off for this tailnet. Turn them on in the DNS page of the tailscale admin console")})
		return 0, nil
	}

	if n.cert.name != dnsName {
		// the old name's listeners go now, the new name needs its own cert
		n.stopServing()
		n.cert = certState{name: dnsName, dir: n.srv.Dir}
	}
	if !m.renew(ctx, lc, &n.cert, &n.box) {
		return time.Until(n.cert.next), nil
	}

	if n.hs == nil {
		ln, err := n.srv.Listen("tcp", ":443")
		if err != nil {
			return 0, err
		}
		n.hs = server(dnsName, tailnet, tailnetConn(lc))
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
	case n.cert.err != nil:
		n.to(Status{State: CertPending, Err: n.cert.err, RetryAt: n.cert.next})
	case funnelErr != nil:
		n.to(Status{State: FunnelNotAllowed, Err: funnelErr})
	default:
		n.to(Status{State: Online})
	}
	return min(time.Until(n.cert.next), m.serveHosted(ctx, n, lc, st)), nil
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
	for id, h := range n.serving {
		h.close()
		if !n.shuttingDown {
			h.to(Status{State: Starting})
		}
		delete(n.serving, id)
	}
}

// renew fetches c's cert when it's due, and is false while there's no working cert
func (m *Manager) renew(ctx context.Context, lc *local.Client, c *certState, b *box) bool {
	if !time.Now().Before(c.next) {
		if !c.ok {
			b.to(Status{State: CertPending, Err: c.err})
		}
		err := m.cert(ctx, lc, c.dir, c.name)
		if ctx.Err() != nil {
			return false
		}
		c.record(err, b)
	}
	if !c.ok {
		b.to(Status{State: CertPending, Err: c.err, RetryAt: c.next})
	}
	return c.ok
}

// record notes how fetching the cert went and when to look again
func (c *certState) record(err error, b *box) {
	if err != nil {
		c.err, c.wait = err, min(max(2*c.wait, time.Minute), time.Hour)
		slog.Warn("fetching cert failed", b.key, b.name, "error", err.Error(), "retry_in", c.wait.String())
	} else {
		c.ok, c.err, c.wait = true, nil, 0
	}
	// a cert that works is checked hourly, tsnet renews it when it's close to expiring
	c.next = time.Now().Add(cmp.Or(c.wait, time.Hour))
}

// cert gets a cert for name, one node at a time. After a failure the next node
// waits a bit, so nodes retrying together don't all reach let's encrypt at once
func (m *Manager) cert(ctx context.Context, lc *local.Client, dir, name string) error {
	// tailscale answers from its cache while the cached cert is valid, renewing it in the
	// background, so only a cert that has to come from let's encrypt waits its turn
	if cached(dir, name) {
		_, _, err := lc.CertPair(ctx, name)
		return err
	}
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

// cached is whether tailscale has a valid cert for name in the node's state dir
func cached(dir, name string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "certs", name+".crt"))
	if err != nil {
		return false
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return false
	}
	c, err := x509.ParseCertificate(block.Bytes)
	return err == nil && time.Now().Before(c.NotAfter)
}

// login logs the node in with a new auth key. Its state dir keeps its machine key,
// so it comes back as the same device if that still exists
func (m *Manager) login(ctx context.Context, n *Node, lc *local.Client, key string) error {
	n.mu.Lock()
	n.loginErr = nil
	n.mu.Unlock()
	if err := lc.Start(ctx, ipn.Options{AuthKey: key}); err != nil {
		return err
	}
	return lc.StartLoginInteractive(ctx)
}

// Login logs a logged out app node back in
func (m *Manager) Login(ctx context.Context, id string) error {
	n := m.Get(id)
	if n == nil || n.Status().State != LoggedOut {
		return errors.New("that node isn't logged out")
	}
	lc, err := n.srv.LocalClient()
	if err != nil {
		return err
	}
	key, err := m.api.authKey(ctx)
	if err != nil {
		return err
	}
	return m.login(ctx, n, lc, key)
}

// tailnetConn looks up which device a tailnet connection came from, once
func tailnetConn(lc *local.Client) func(net.Conn) *conn {
	return func(c net.Conn) *conn {
		return &conn{peer: sync.OnceValues(func() (*Peer, error) { return whois(lc, c.RemoteAddr().String()) })}
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

// retry runs f until it works or ctx ends, so a tailscale api blip doesn't fail anything for good
func retry(ctx context.Context, b *box, f func() error) error {
	for wait := 5 * time.Second; ; wait = min(2*wait, 5*time.Minute) {
		err := f()
		if err == nil || ctx.Err() != nil {
			return cmp.Or(ctx.Err(), err)
		}
		slog.Warn("tailscale api call failed", b.key, b.name, "error", err.Error(), "retry_in", wait.String())
		b.to(Status{State: Starting, Err: err, RetryAt: time.Now().Add(wait)})
		if !sleep(ctx, wait) {
			return ctx.Err()
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// Unused lists the state dirs in nodes/ of the ids not in inUse, which are the
// apps that could use a node, and then the tailscale services tailgate
// made that it doesn't serve, like svc:photos
func (m *Manager) Unused(inUse map[string]bool) ([]string, error) {
	var names []string
	entries, err := os.ReadDir(m.nodesDir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() && !inUse[e.Name()] {
			names = append(names, e.Name())
		}
	}
	hosted := m.hostedNames()
	for _, name := range m.api.defined() {
		if !hosted[name] {
			names = append(names, name)
		}
	}
	return names, nil
}

// Prune forgets the unused nodes and tailscale services that were deleted in the
// tailscale admin console, dropping a node's state dir. It's true when it forgot any
func (m *Manager) Prune(ctx context.Context, inUse map[string]bool) (bool, error) {
	unused, err := m.Unused(inUse)
	if err != nil || len(unused) == 0 {
		return false, err
	}
	devices, err := m.api.devices(ctx)
	if err != nil {
		return false, err
	}
	// the tailgate node is always there, so an empty list is a mistake that would drop every unused node
	if len(devices) == 0 {
		return false, errors.New("the tailscale api listed no devices")
	}
	services, err := m.api.services(ctx)
	if err != nil {
		return false, err
	}
	pruned := false
	var errs []error
	for _, name := range unused {
		if strings.HasPrefix(name, "svc:") {
			if !services[name] {
				m.api.forget(name)
				slog.Info("tailscale service was deleted", "service", name)
				pruned = true
			}
			continue
		}
		dir := filepath.Join(m.nodesDir(), name)
		id, err := deviceID(dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		if devices[id] {
			continue
		}
		m.mu.Lock()
		// a node that started meanwhile uses the dir
		if m.nodes[name] == nil {
			err = os.RemoveAll(dir)
		}
		m.mu.Unlock()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		slog.Info("node's device was deleted, so its state was too", "node", name)
		pruned = true
	}
	return pruned, errors.Join(errs...)
}

// deviceID reads the stable id of the device a node's state dir logs in as,
// "" when it never joined
func deviceID(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "tailscaled.state"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// the state file maps keys to json, and _profiles lists the logins with their device ids
	var state map[ipn.StateKey][]byte
	if err := json.Unmarshal(b, &state); err != nil {
		return "", err
	}
	if state[ipn.KnownProfilesStateKey] == nil {
		return "", nil
	}
	var profiles map[ipn.ProfileID]ipn.LoginProfile
	if err := json.Unmarshal(state[ipn.KnownProfilesStateKey], &profiles); err != nil {
		return "", err
	}
	// _current-profile names the profile's own state key
	for _, p := range profiles {
		if p.Key == ipn.StateKey(state[ipn.CurrentProfileStateKey]) {
			return string(p.NodeID), nil
		}
	}
	return "", errors.New("the node's state has no current profile")
}
