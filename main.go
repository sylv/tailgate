package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/sylv/tailgate/docker"
	"github.com/sylv/tailgate/geo"
	"github.com/sylv/tailgate/node"
	"github.com/sylv/tailgate/rules"
	"github.com/sylv/tailgate/service"
	"github.com/sylv/tailgate/store"
)

// version is like v1-1.106.1-a1b2c3d, the major version, tailscale version and commit, set by ci
var version = "dev"

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	args := os.Args[1:]
	var err error
	switch {
	case len(args) == 0:
		err = run()
	case len(args) == 1 && args[0] == "docker-proxy":
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()
		// 65532 is the image's nonroot user, which tailgate runs as
		err = docker.Proxy(ctx, "/run/tailgate/docker.sock", "/var/run/docker.sock", 65532)
	case len(args) == 2 && args[0] == "reset-key":
		err = withUser(args[1], func(db *store.Store, id string) error {
			key, err := db.ResetKeys(id)
			fmt.Println(key)
			return err
		})
	case len(args) == 3 && args[0] == "set-admin" && (args[2] == "true" || args[2] == "false"):
		err = withUser(args[1], func(db *store.Store, id string) error { return db.SetAdmin(id, args[2] == "true") })
	default:
		fmt.Fprintln(os.Stderr, "usage: tailgate [docker-proxy | reset-key <user> | set-admin <user> true|false]")
		os.Exit(2)
	}
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

func dataDir() string { return cmp.Or(os.Getenv("TAILGATE_DATA_DIR"), "./data") }

// withUser opens the database directly for the commands, so they work while tailgate runs
func withUser(name string, fn func(*store.Store, string) error) error {
	db, err := store.Open(filepath.Join(dataDir(), "tailgate.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	id, err := db.UserIDByName(name)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("there's no user called %q", name)
	}
	if err != nil {
		return err
	}
	return fn(db, id)
}

func run() error {
	slog.Info("starting tailgate", "version", version)
	authKey := os.Getenv("TS_AUTHKEY")
	if authKey == "" {
		return errors.New("TS_AUTHKEY is required")
	}
	// tsnet falls back to TS_AUTHKEY itself, which would let removing unused nodes register new ones
	os.Unsetenv("TS_AUTHKEY")
	hostname := cmp.Or(os.Getenv("TAILGATE_HOSTNAME"), "tailgate")
	dc, err := docker.New(cmp.Or(os.Getenv("DOCKER_HOST"), "unix:///var/run/docker.sock"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir(), 0o700); err != nil {
		return err
	}
	db, err := store.Open(filepath.Join(dataDir(), "tailgate.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	nodes := node.NewManager(dataDir(), authKey)
	defer nodes.Close()
	g := newGate(db, nodes)
	g.geo = geo.Open(dataDir())
	// db-ip's license needs attribution
	slog.Info("IP geolocation by DB-IP", "url", "https://db-ip.com")
	go g.geo.Update(ctx)
	g.hosts = func(name string) string {
		n := nodes.Self()
		if name != "" {
			n = nodes.Get(name)
		}
		if n == nil {
			return ""
		}
		return n.Status().DNSName
	}
	g.peers = func(ctx context.Context) ([]node.Peer, error) { return nodes.Self().Peers(ctx) }
	self := nodes.StartSelf(hostname, g.web(false), g.web(true))
	select {
	case <-self.Registered():
	case <-self.Done():
		return fmt.Errorf("the tailgate node couldn't start: %w", self.Status().Err)
	case <-ctx.Done():
		return nil
	}
	has, err := db.HasUsers()
	if err != nil {
		return err
	}
	if !has {
		slog.Info("open the setup link to create the first admin", "url", "https://"+g.hosts("")+"/setup?token="+g.setupToken)
	}

	changed := make(chan struct{}, 1)
	trigger := func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	g.refresh = trigger
	trigger()
	// the hourly self check, since docker can become unreadable without an event
	go func() {
		for range time.Tick(time.Hour) {
			trigger()
		}
	}()
	go func() {
		for ctx.Err() == nil {
			err := dc.Watch(ctx, trigger)
			if ctx.Err() == nil {
				slog.Error("watching docker events failed", "error", err.Error())
				// events may have been missed while disconnected
				trigger()
				time.Sleep(5 * time.Second)
			}
		}
	}()
	for {
		select {
		case <-changed:
			containers, err := dc.Containers(ctx)
			g.mu.Lock()
			g.dockerErr = err
			g.mu.Unlock()
			if err != nil {
				// keep what's running, a docker hiccup shouldn't take every service down
				slog.Error("listing containers failed", "error", err.Error())
				continue
			}
			g.reconcile(service.FromContainers(containers, os.Getenv("TAILGATE_NETWORK")))
		case <-ctx.Done():
			slog.Info("shutting down")
			return nil
		}
	}
}

type gate struct {
	db         *store.Store
	nodes      *node.Manager
	setupToken string
	codes      tokens[code]
	oidcCodes  tokens[oidcCode]
	access     tokens[accessToken]
	transport  *http.Transport
	rp         *httputil.ReverseProxy
	// hosts gives a service node's DNS name, or the tailgate node's for "", and "" until it has joined
	hosts func(name string) string
	peer  func(context.Context) (*node.Peer, error)
	peers func(context.Context) ([]node.Peer, error)
	// refresh reads the containers again
	refresh func()
	// geo is nil in tests
	geo *geo.DB

	mu       sync.RWMutex
	services []service.Service
	routes   map[string]service.Service
	unused   []string
	// dockerErr is set while listing containers fails
	dockerErr error
	rules     *rules.Set
	// rulesErr is set when the saved rules can't be loaded, which blocks every service request
	rulesErr error
	signer   *rsa.PrivateKey
}

func newGate(db *store.Store, nodes *node.Manager) *gate {
	g := &gate{db: db, nodes: nodes, setupToken: rand.Text(), peer: node.PeerOf, refresh: func() {}, routes: map[string]service.Service{}}
	g.transport, g.rp = newProxy()
	g.loadRules()
	return g
}

func (g *gate) loadRules() {
	list, err := g.db.Rules()
	var set *rules.Set
	if err == nil {
		var errs []rules.Error
		if set, errs = rules.Compile(list); errs != nil {
			for _, e := range errs {
				err = errors.Join(err, e)
			}
		}
	}
	if err != nil {
		slog.Error("loading the rules failed, so every service request is blocked", "error", err.Error())
	}
	g.mu.Lock()
	g.rules, g.rulesErr = set, err
	g.mu.Unlock()
}

func (g *gate) ruleSet() (*rules.Set, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.rules, g.rulesErr
}

// route returns a service that's routed, which means its labels are valid
func (g *gate) route(name string) (service.Service, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	s, ok := g.routes[name]
	return s, ok
}

func (g *gate) routed() []service.Service {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return slices.SortedFunc(maps.Values(g.routes), func(a, b service.Service) int { return cmp.Compare(a.Name, b.Name) })
}

func (g *gate) snapshot() ([]service.Service, []string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.services, g.unused, g.dockerErr
}

// inUse is every name a container has, broken ones too, so their nodes aren't unused
func (g *gate) inUse() map[string]bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	names := map[string]bool{}
	for _, s := range g.services {
		if s.Name != "" {
			names[s.Name] = true
		}
	}
	return names
}

func (g *gate) nodeStatus(name string) (node.Status, bool) {
	if n := g.nodes.Get(name); n != nil {
		return n.Status(), true
	}
	return node.Status{}, false
}

// reconcile routes every valid service, starts its node and stops the rest
func (g *gate) reconcile(services []service.Service) {
	routes := map[string]service.Service{}
	g.mu.RLock()
	old := map[string]string{}
	for _, s := range g.services {
		if s.Err != nil {
			old[s.Container] = s.Err.Error()
		}
	}
	public := map[string]bool{}
	for _, s := range g.routes {
		public[s.Name] = s.Funnel && s.DangerouslyDisableAuth
	}
	g.mu.RUnlock()
	funnel := false
	for _, s := range services {
		if s.Err != nil {
			if old[s.Container] != s.Err.Error() {
				slog.Error("service has errors and isn't routed", "container", s.Container, "service", s.Name, "error", s.Err.Error())
			}
			continue
		}
		if s.Funnel && s.DangerouslyDisableAuth && !public[s.Name] {
			slog.Warn("service is on funnel with auth disabled, so anyone on the internet can use it", "service", s.Name)
		}
		funnel = funnel || s.Funnel
		routes[s.Name] = s
	}

	g.mu.Lock()
	moved := false
	for name, s := range g.routes {
		if routes[name].Addr != s.Addr {
			moved = true
		}
	}
	// update the routes before starting nodes so a new node never routes to nothing
	g.services, g.routes = services, routes
	g.mu.Unlock()
	if moved {
		g.transport.CloseIdleConnections()
	}
	for _, name := range g.nodes.Running() {
		if _, ok := routes[name]; !ok {
			g.nodes.Stop(name)
		}
	}
	for name, s := range routes {
		g.nodes.Start(name, g.serviceHandler(name), s.Funnel)
	}
	// funnel visitors have to reach the login page
	g.nodes.Self().SetFunnel(funnel)

	unused, err := g.nodes.Unused(g.inUse())
	if err != nil {
		slog.Error("listing node state dirs failed", "error", err.Error())
		return
	}
	g.mu.Lock()
	if !slices.Equal(unused, g.unused) && len(unused) > 0 {
		slog.Info("unused nodes", "nodes", unused)
	}
	g.unused = unused
	g.mu.Unlock()
}
