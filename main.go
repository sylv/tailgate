package main

import (
	"cmp"
	"context"
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
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/lmittmann/tint"
	"github.com/sylv/tailgate/app"
	"github.com/sylv/tailgate/docker"
	"github.com/sylv/tailgate/geo"
	"github.com/sylv/tailgate/node"
	"github.com/sylv/tailgate/rules"
	"github.com/sylv/tailgate/store"
)

// version is like v1-1.106.1-a1b2c3d, the major version, tailscale version and commit, set by ci
var version = "dev"

func main() {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cmp.Or(os.Getenv("TAILGATE_LOG_LEVEL"), "info"))); err != nil {
		fmt.Fprintln(os.Stderr, "TAILGATE_LOG_LEVEL must be debug, info, warn or error")
		os.Exit(2)
	}
	var h slog.Handler = tint.NewHandler(os.Stdout, &tint.Options{Level: level, TimeFormat: time.DateTime, NoColor: os.Getenv("NO_COLOR") != ""})
	if json, _ := strconv.ParseBool(os.Getenv("TAILGATE_LOG_JSON")); json {
		h = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	}
	slog.SetDefault(slog.New(h))
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
	secret := os.Getenv("TS_CLIENT_SECRET")
	if secret == "" {
		return errors.New("TS_CLIENT_SECRET is required, an oauth client secret. TS_AUTHKEY isn't used anymore")
	}
	// tsnet falls back to these itself, which would let a removed node log back in
	for _, env := range []string{"TS_CLIENT_SECRET", "TS_CLIENT_ID", "TS_AUTHKEY", "TS_AUTH_KEY"} {
		os.Unsetenv(env)
	}
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

	api, err := node.NewAPI(ctx, secret)
	if err != nil {
		return err
	}
	nodes := node.NewManager(dataDir(), api)
	defer nodes.Close()
	g := newGate(db, nodes)
	g.geo = geo.Open(dataDir())
	// db-ip's license needs attribution
	slog.Info("IP geolocation by DB-IP", "url", "https://db-ip.com")
	go g.geo.Update(ctx)
	g.hosts = func(name string) string {
		if name != "" {
			st, _ := nodes.Status(name)
			return st.DNSName
		}
		if self := nodes.Self(); self != nil {
			return self.Status().DNSName
		}
		return ""
	}
	g.peers = func(ctx context.Context) ([]node.Peer, error) { return nodes.Self().Peers(ctx) }
	self := nodes.StartSelf(hostname, g.web(false), g.web(true))
	select {
	case <-self.Ready():
	case <-self.Done():
		return fmt.Errorf("the tailgate node couldn't start: %w", self.Status().Err)
	case <-ctx.Done():
		return nil
	}
	setupKey, err := db.StartSetup()
	if err != nil {
		return err
	}
	if setupKey == "" {
		slog.Info("tailgate is ready", "admin", "https://"+g.hosts("")+"/admin")
	} else {
		slog.Warn("setup isn't done, so apps and funnel stay off until you open the setup link", "url", "https://"+g.hosts("")+"/setup?key="+setupKey)
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
	// unused nodes and tailscale services are deleted in the tailscale admin console, which tailgate only sees by asking
	go func() {
		lastErr := ""
		for range time.Tick(30 * time.Second) {
			pruned, err := nodes.Prune(ctx, g.inUse())
			if msg := fmt.Sprint(err); err != nil && msg != lastErr {
				slog.Warn("checking for deleted devices failed", "error", msg)
			}
			lastErr = fmt.Sprint(err)
			if pruned {
				trigger()
			}
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
				// keep what's running, a docker hiccup shouldn't take every app down
				slog.Error("listing containers failed", "error", err.Error())
				continue
			}
			g.reconcile(app.FromContainers(containers, os.Getenv("TAILGATE_NETWORK")))
		case <-ctx.Done():
			slog.Info("shutting down")
			return nil
		}
	}
}

type gate struct {
	db        *store.Store
	nodes     *node.Manager
	codes     tokens[code]
	oidcCodes tokens[oidcCode]
	access    tokens[accessToken]
	transport *http.Transport
	rp        *httputil.ReverseProxy
	// hosts gives an app node's DNS name, or the tailgate node's for "", and "" until it has joined
	hosts func(name string) string
	peer  func(context.Context) (*node.Peer, error)
	peers func(context.Context) ([]node.Peer, error)
	// refresh reads the containers again
	refresh func()
	// geo is nil in tests
	geo *geo.DB

	mu     sync.RWMutex
	apps   []app.App
	routes map[string]app.App
	unused []string
	// dockerErr is set while listing containers fails
	dockerErr error
	rules     *rules.Set
	// rulesErr is set when the saved rules can't be loaded, which blocks every app request
	rulesErr error
	signer   *rsa.PrivateKey
}

func newGate(db *store.Store, nodes *node.Manager) *gate {
	g := &gate{db: db, nodes: nodes, peer: node.PeerOf, refresh: func() {}, routes: map[string]app.App{}}
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
		slog.Error("loading the rules failed, so every app request is blocked", "error", err.Error())
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

// route returns an app that's routed, which means its labels are valid
func (g *gate) route(id string) (app.App, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	s, ok := g.routes[id]
	return s, ok
}

// hostname is what people see an app called, its id while it isn't routed
func (g *gate) hostname(id string) string {
	if s, ok := g.route(id); ok {
		return s.Hostname
	}
	return id
}

func (g *gate) routed() []app.App {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return slices.SortedFunc(maps.Values(g.routes), func(a, b app.App) int { return cmp.Compare(a.ID, b.ID) })
}

func (g *gate) snapshot() ([]app.App, []string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.apps, g.unused, g.dockerErr
}

// inUse is every id that could use a node, broken apps too, so their nodes aren't unused
func (g *gate) inUse() map[string]bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	names := map[string]bool{}
	for _, s := range g.apps {
		if s.ID != "" && (s.Err != nil || s.Funnel) {
			names[s.ID] = true
		}
	}
	return names
}

// reconcile routes every valid app, starts its node and stops the rest
func (g *gate) reconcile(apps []app.App) {
	routes := map[string]app.App{}
	g.mu.RLock()
	old := map[string]string{}
	for _, s := range g.apps {
		if s.Err != nil {
			old[s.Container] = s.Err.Error()
		}
	}
	public := map[string]bool{}
	for _, s := range g.routes {
		public[s.ID] = s.Funnel && s.DangerouslyDisableAuth
	}
	g.mu.RUnlock()
	pending, err := g.db.SetupPending()
	if err != nil {
		slog.Error("checking setup failed, so nothing is routed", "error", err.Error())
	}
	funnel := false
	for _, s := range apps {
		if s.Err != nil {
			if old[s.Container] != s.Err.Error() {
				slog.Error("app has errors and isn't routed", "container", s.Container, "app", s.ID, "error", s.Err.Error())
			}
			continue
		}
		if s.Funnel && s.DangerouslyDisableAuth && !public[s.ID] {
			slog.Warn("app is on funnel with auth disabled, so anyone on the internet can use it", "app", s.ID)
		}
		if pending || err != nil {
			continue
		}
		funnel = funnel || s.Funnel
		routes[s.ID] = s
	}

	g.mu.Lock()
	moved := false
	for name, s := range g.routes {
		if routes[name].Addr != s.Addr {
			moved = true
		}
	}
	// update the routes before starting nodes so a new node never routes to nothing
	g.apps, g.routes = apps, routes
	g.mu.Unlock()
	if moved {
		g.transport.CloseIdleConnections()
	}
	for _, name := range g.nodes.Running() {
		if s, ok := routes[name]; !ok || !s.Funnel {
			g.nodes.Stop(name)
		}
	}
	for _, name := range g.nodes.Hosted() {
		if s, ok := routes[name]; !ok || s.Funnel {
			g.nodes.Unhost(name)
		}
	}
	for name, s := range routes {
		// funnel only works on a node's own name, the rest are tailscale services on the tailgate node
		if s.Funnel {
			g.nodes.Start(name, s.Hostname, g.appHandler(name), s.Funnel)
		} else {
			g.nodes.Host(name, s.Hostname, g.appHandler(name))
		}
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
		slog.Info("unused nodes and tailscale services", "unused", unused)
	}
	g.unused = unused
	g.mu.Unlock()
}
