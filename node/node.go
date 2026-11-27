package node

import (
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
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

	"github.com/sylv/tailgate/store"
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
	// Funnel is whether the node's funnel listener is on
	Funnel bool
}

func (s Status) same(o Status) bool {
	return s.State == o.State && s.DNSName == o.DNSName && s.SignCommand == o.SignCommand && fmt.Sprint(s.Err) == fmt.Sprint(o.Err) &&
		s.RetryAt.Equal(o.RetryAt) && s.Service == o.Service && s.Funnel == o.Funnel
}

// box holds a node's or a tailscale service's status, logs its changes and tells the manager
type box struct {
	// key is "node" or "service", for the logs
	key, name string
	changed   func()

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
	if !before.same(after) && b.changed != nil {
		b.changed()
	}
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

// to moves to st, keeping the DNS name and funnel
func (b *box) to(st Status) {
	b.set(func(s *Status) {
		st.DNSName, st.Funnel = s.DNSName, s.Funnel
		*s = st
	})
}

func (b *box) fail(err error) { b.to(Status{State: Failed, Err: err}) }

// certState is how getting the cert for name is going
type certState struct {
	name string
	ok   bool
	err  error
	wait time.Duration
	next time.Time
}

type Node struct {
	box
	// id is the app's, "" for the tailgate node
	id            string
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
	was := n.funnel
	n.funnel = on
	n.mu.Unlock()
	if was == on {
		return
	}
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
	db      *store.Store
	// changed is called whenever a node's or tailscale service's status changes, and forgot when unused nodes are forgotten
	changed, forgot func()
	// new certs come from let's encrypt two at a time, see cert
	certSem chan struct{}

	mu       sync.Mutex
	self     *Node
	nodes    map[string]*Node
	untagged bool
	// devices maps each app that has a node, running or not, to its device id, "" until it joined
	devices map[string]string
	// peers is the device ids the tailgate node can see, nil until it has looked
	peers map[string]bool
	// unused is the apps whose nodes no container uses, see Unused
	unused []string
	// problem is set while the tailgate node can't see a running funnel node, so deleted devices can't be noticed
	problem string
}

func NewManager(dataDir string, api *API, db *store.Store, changed, forgot func()) (*Manager, error) {
	devices, err := db.Nodes()
	if err != nil {
		return nil, err
	}
	return &Manager{dataDir: dataDir, api: api, db: db, changed: changed, forgot: forgot, certSem: make(chan struct{}, 2), nodes: map[string]*Node{}, devices: devices}, nil
}

func (m *Manager) nodesDir() string { return filepath.Join(m.dataDir, "nodes") }

// StartSelf starts the tailgate node. Funnel connections go to their own handler
func (m *Manager) StartSelf(hostname string, tailnet, funnel http.Handler) *Node {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.self = m.start("", hostname, filepath.Join(m.dataDir, "tailgate"), tailnet, funnel)
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
		m.nodes[id] = m.start(id, hostname, filepath.Join(m.nodesDir(), id), h, h)
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

// start runs a node, m.mu is held
func (m *Manager) start(id, name, dir string, tailnet, funnel http.Handler) *Node {
	ctx, cancel := context.WithCancel(context.Background())
	n := &Node{
		box:           box{key: "node", name: name, status: Status{State: Starting}, changed: m.changed},
		id:            id,
		self:          id == "",
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
	// its own. only nodes that never joined get one, a logged out app node waits for Login
	n.fresh = m.devices[id] == ""
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
	if _, ok := m.device(n.id); !ok && m.saveDevice(n.id, "") != nil {
		n.fail(errors.New("saving the node failed"))
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
	go m.watch(ctx, n, lc)
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
// changes, which covers its login, tags, signature, name and https and funnel access.
// The tailgate node also watches its peers, to notice deleted devices
func (m *Manager) watch(ctx context.Context, n *Node, lc *local.Client) {
	mask := ipn.NotifyInitialState
	peers := make(chan struct{}, 1)
	if n.self {
		mask |= ipn.NotifyPeerChanges
		go m.watchPeers(ctx, lc, peers)
	}
	for ctx.Err() == nil {
		if w, err := lc.WatchIPNBus(ctx, mask); err == nil {
			select {
			case peers <- struct{}{}:
			default:
			}
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
				if len(note.PeersChanged) > 0 || len(note.PeersRemoved) > 0 {
					select {
					case peers <- struct{}{}:
					default:
					}
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
	if id := string(st.Self.ID); id != "" {
		if old, _ := m.device(n.id); old != id {
			if err := m.saveDevice(n.id, id); err != nil {
				return 0, err
			}
		}
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
		n.cert = certState{name: dnsName}
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
			n.set(func(s *Status) { s.Funnel = true })
			slog.Info("funnel on", "node", n.name)
		}
	case !want && n.fs != nil:
		go shutdown(n.fs)
		n.fs = nil
		n.set(func(s *Status) { s.Funnel = false })
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

// stopServing closes the node's servers. They finish their requests in the
// background, except when tailgate shuts down, which waits for them
func (n *Node) stopServing() {
	var servers []*http.Server
	if n.hs != nil {
		servers = append(servers, n.hs)
		n.hs = nil
	}
	if n.fs != nil {
		servers = append(servers, n.fs)
		n.fs = nil
		n.set(func(s *Status) { s.Funnel = false })
		slog.Info("funnel off", "node", n.name)
	}
	for id, h := range n.serving {
		if h.hs != nil {
			servers = append(servers, h.hs)
		} else if h.ln != nil {
			h.ln.Close()
		}
		h.hs, h.ln = nil, nil
		if !n.shuttingDown {
			h.to(Status{State: Starting})
		}
		delete(n.serving, id)
	}
	var wg sync.WaitGroup
	for _, s := range servers {
		wg.Go(func() { shutdown(s) })
	}
	if n.shuttingDown {
		wg.Wait()
	}
}

// shutdown lets open requests finish for a few seconds, then cuts off the rest,
// like websockets and event streams that never finish
func shutdown(s *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.Shutdown(ctx) != nil {
		s.Close()
	}
}

// renew fetches c's cert when it's due, and is false while there's no working cert
func (m *Manager) renew(ctx context.Context, lc *local.Client, c *certState, b *box) bool {
	if !time.Now().Before(c.next) {
		if !c.ok {
			b.to(Status{State: CertPending, Err: c.err})
		}
		err := m.cert(ctx, lc, c.name)
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

// cert gets a cert for name. tailscale answers from its cache while it has a valid cert,
// renewing it in the background, so only a cert tailgate hasn't fetched before waits its
// turn. On a first start every app needs one, and let's encrypt fails orders that all
// arrive at once
func (m *Manager) cert(ctx context.Context, lc *local.Client, name string) error {
	if valid, err := m.db.CertValid(name); err != nil {
		return err
	} else if !valid {
		select {
		case m.certSem <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		defer func() { <-m.certSem }()
	}
	// tailscale waits on let's encrypt with no deadline, so one stuck order would hold up every other node
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	certPEM, _, err := lc.CertPair(ctx, name)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return errors.New("tailscale sent a cert that isn't PEM")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	return m.db.SaveCert(name, c.NotAfter)
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

// tailnetConn looks up which device a tailnet connection came from, once it's found
func tailnetConn(lc *local.Client) func(net.Conn) *conn {
	return func(c net.Conn) *conn {
		var mu sync.Mutex
		var found *Peer
		return &conn{peer: func() (*Peer, error) {
			mu.Lock()
			defer mu.Unlock()
			if found != nil {
				return found, nil
			}
			p, err := whois(lc, c.RemoteAddr().String())
			found = p
			return p, err
		}}
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
// funnel and for a device tailscale doesn't know, which is anonymous. A found
// device is kept for the connection, since a connection can't change which
// device it comes from
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
	if errors.Is(err, local.ErrPeerNotFound) {
		return nil, nil
	}
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

func (m *Manager) device(id string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	return d, ok
}

func (m *Manager) saveDevice(id, device string) error {
	if err := m.db.SaveNode(id, device); err != nil {
		return err
	}
	m.mu.Lock()
	m.devices[id] = device
	m.mu.Unlock()
	return nil
}

// Unused lists the apps whose nodes no container uses, inUse being the apps that
// could use one. It forgets the ones whose devices were deleted first
func (m *Manager) Unused(inUse map[string]bool) []string {
	m.mu.Lock()
	m.unused = m.unused[:0]
	for id := range m.devices {
		if id != "" && !inUse[id] && m.nodes[id] == nil {
			m.unused = append(m.unused, id)
		}
	}
	slices.Sort(m.unused)
	m.mu.Unlock()
	m.prune()
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.unused)
}

// Problem is the self check, "" when the tailgate node can see the funnel nodes
func (m *Manager) Problem() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.problem
}

// watchPeers reads the devices the tailgate node can see whenever they change
func (m *Manager) watchPeers(ctx context.Context, lc *local.Client, changed <-chan struct{}) {
	for {
		select {
		case <-changed:
		case <-ctx.Done():
			return
		}
		st, err := lc.Status(ctx)
		if err != nil {
			continue
		}
		peers := map[string]bool{}
		for _, p := range st.Peer {
			peers[string(p.ID)] = true
		}
		m.mu.Lock()
		same := maps.Equal(peers, m.peers)
		m.peers = peers
		m.mu.Unlock()
		if !same && m.prune() {
			m.forgot()
		}
		// a busy tailnet changes peers constantly, so this looks at most every few seconds
		sleep(ctx, 5*time.Second)
	}
}

// prune forgets the unused nodes whose devices were deleted in tailscale, which is when the
// tailgate node stops seeing them, dropping their state dirs. That relies on a grant from
// tag:tailgate to tag:tailgate, so nothing is pruned while a running node is missing. It's
// true when it forgot any
func (m *Manager) prune() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.peers == nil {
		return false
	}
	m.problem = ""
	for id, n := range m.nodes {
		if d := m.devices[id]; d != "" && !m.peers[d] && n.Status().State == Online {
			m.problem = fmt.Sprintf("The tailgate node can't see %s, so it can't notice deleted devices. Add a grant from %s to %s on port 443 to your tailnet policy", n.name, tag, tag)
			return false
		}
	}
	pruned := false
	m.unused = slices.DeleteFunc(m.unused, func(id string) bool {
		if m.peers[m.devices[id]] || m.nodes[id] != nil {
			return false
		}
		if err := errors.Join(os.RemoveAll(filepath.Join(m.nodesDir(), id)), m.db.DeleteNode(id)); err != nil {
			slog.Error("forgetting a deleted node failed", "node", id, "error", err.Error())
			return false
		}
		delete(m.devices, id)
		slog.Info("node's device was deleted, so its state was too", "node", id)
		pruned = true
		return true
	})
	return pruned
}
