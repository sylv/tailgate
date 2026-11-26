package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sylv/tailgate/node"
	"github.com/sylv/tailgate/rules"
	"github.com/sylv/tailgate/store"
)

const csp = "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

// web serves the tailgate node: the login page, the dashboard, setup and the
// admin pages. Setup and the admin pages aren't registered at all for funnel
func (g *gate) web(funnel bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", g.page)
	mux.HandleFunc("GET /login", g.login)
	mux.HandleFunc("GET /assets/", func(w http.ResponseWriter, r *http.Request) {
		a := uiAssets()[r.URL.Path]
		if a == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		a.serve(w, r)
	})

	mux.HandleFunc("GET /api/me", g.api(false, g.me))
	mux.HandleFunc("POST /api/login", g.api(false, g.signIn))
	mux.HandleFunc("POST /api/logout", g.api(false, g.signOut))
	g.oidc(mux)
	if !funnel {
		g.tailnetOnly(mux)
	}

	// every service shares the tailnet's site, so SameSite cookies don't stop one
	// from posting here. this refuses cross origin writes to every route, while
	// apps swapping oidc codes send no origin and pass
	h := http.NewCrossOriginProtection().Handler(mux)
	return requests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		h.ServeHTTP(w, r)
	}))
}

func (g *gate) tailnetOnly(mux *http.ServeMux) {
	for _, path := range []string{"/setup", "/admin", "/admin/"} {
		mux.HandleFunc("GET "+path, g.page)
	}
	mux.HandleFunc("POST /api/setup", g.api(false, g.setup))
	mux.HandleFunc("GET /api/admin/users", g.api(true, func(http.ResponseWriter, *http.Request, *identity) (any, error) { return g.db.Users() }))
	mux.HandleFunc("POST /api/admin/users", g.api(true, g.saveUser))
	mux.HandleFunc("PUT /api/admin/users/{id}", g.api(true, g.saveUser))
	mux.HandleFunc("DELETE /api/admin/users/{id}", g.api(true, g.deleteUser))
	mux.HandleFunc("POST /api/admin/users/{id}/keys", g.api(true, g.addKey))
	mux.HandleFunc("POST /api/admin/users/{id}/reset-keys", g.api(true, g.resetKeys))
	mux.HandleFunc("DELETE /api/admin/keys/{id}", g.api(true, func(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
		return nil, g.db.RevokeKey(r.PathValue("id"))
	}))
	mux.HandleFunc("POST /api/admin/users/{id}/logins", g.api(true, g.linkLogin))
	mux.HandleFunc("DELETE /api/admin/logins/{id}", g.api(true, func(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			return nil, store.ErrNotFound
		}
		return nil, g.db.UnlinkLogin(id)
	}))
	mux.HandleFunc("GET /api/admin/logins", g.api(true, g.tailnetLogins))
	mux.HandleFunc("GET /api/admin/services", g.api(true, g.adminServices))
	mux.HandleFunc("POST /api/admin/remove-unused-nodes", g.api(true, g.removeUnused))
	mux.HandleFunc("GET /api/admin/rules", g.api(true, g.listRules))
	mux.HandleFunc("PUT /api/admin/rules", g.api(true, g.saveRules))
	mux.HandleFunc("POST /api/admin/rules/check", g.api(true, checkRule))
	mux.HandleFunc("GET /api/admin/rules/schema", g.api(true, g.ruleSchema))
	mux.HandleFunc("GET /api/admin/oidc-clients", g.api(true, g.oidcClients))
	mux.HandleFunc("POST /api/admin/oidc-clients", g.api(true, g.saveOIDCClient))
	mux.HandleFunc("PUT /api/admin/oidc-clients/{id}", g.api(true, g.saveOIDCClient))
	mux.HandleFunc("DELETE /api/admin/oidc-clients/{id}", g.api(true, func(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
		return nil, g.db.DeleteOIDCClient(r.PathValue("id"))
	}))
}

func (g *gate) page(w http.ResponseWriter, r *http.Request) {
	index := uiAssets()["/index.html"]
	if index == nil {
		plain(w, r, http.StatusServiceUnavailable, "The UI wasn't built into this binary")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	index.serve(w, r)
}

// login skips the login page when the visitor is already signed in and can
// reach the service they came from
func (g *gate) login(w http.ResponseWriter, r *http.Request) {
	id, err := g.identify(w, r, "")
	if err != nil {
		slog.Error("reading credentials failed", "request_id", requestOf(r.Context()).id, "error", err.Error())
		plain(w, r, http.StatusInternalServerError, "Signing in failed")
		return
	}
	to := r.URL.Query().Get("to")
	if id != nil && to == "" {
		redirect(w, "/")
		return
	}
	if id != nil {
		if t, err := g.target(to); err == nil && id.user.HasAccess(t.service) {
			redirect(w, g.continueURL(id, t))
			return
		}
	}
	g.page(w, r)
}

type target struct {
	service, host, path string
}

// target reads ?to=, a service name and a path. The URL is built from the
// service's own host so it can't point anywhere else
func (g *gate) target(to string) (target, error) {
	name, path, _ := strings.Cut(to, ":")
	if _, ok := g.route(name); !ok {
		return target{}, errors.New("There's no service called " + name)
	}
	// a path starting with // or /\ is another host to a browser
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, "/\\") {
		return target{}, errors.New("The link you followed is broken")
	}
	host := g.hosts(name)
	if host == "" {
		return target{}, errors.New(name + " isn't online yet")
	}
	return target{service: name, host: host, path: path}, nil
}

func (g *gate) continueURL(id *identity, t target) string {
	if id.via == "tailnet" {
		// the service signs a linked login in by itself
		return "https://" + t.host + t.path
	}
	c := g.codes.add(code{service: t.service, path: t.path, userID: id.user.ID, keyID: id.keyID, parent: id.session}, time.Minute)
	return "https://" + t.host + callbackPath + "?code=" + c
}

type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func badRequest(msg string) error { return &apiError{http.StatusBadRequest, msg} }

// api runs fn for a JSON endpoint
func (g *gate) api(admin bool, fn func(http.ResponseWriter, *http.Request, *identity) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		id, err := g.identify(w, r, "")
		if err == nil && admin && (id == nil || !id.user.Admin) {
			err = &apiError{http.StatusForbidden, "Only admins can do that"}
		}
		var v any
		if err == nil {
			v, err = fn(w, r, id)
		}
		var ae *apiError
		switch {
		case err == nil:
			if v == nil {
				v = struct{}{}
			}
			writeJSON(w, http.StatusOK, v)
		case errors.As(err, &ae):
			writeJSON(w, ae.status, map[string]string{"error": ae.msg})
		case errors.Is(err, store.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "That doesn't exist anymore"})
		default:
			slog.Error("api request failed", "request_id", requestOf(r.Context()).id, "path", r.URL.Path, "error", err.Error())
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Something went wrong, the request ID is " + requestOf(r.Context()).id})
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v); err != nil {
		return badRequest("The request body isn't valid JSON")
	}
	return nil
}

type link struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func (g *gate) me(_ http.ResponseWriter, r *http.Request, id *identity) (any, error) {
	res := struct {
		User     *store.User `json:"user"`
		Via      string      `json:"via,omitempty"`
		Login    string      `json:"login,omitempty"`
		Services []link      `json:"services"`
		Target   string      `json:"target,omitempty"`
		Error    string      `json:"error,omitempty"`
	}{Services: []link{}}
	// client is the oidc client the login page is signing in to
	var client *store.OIDCClient
	if clientID := r.URL.Query().Get("client"); clientID != "" {
		var err error
		if client, err = g.db.OIDCClient(clientID); err != nil {
			return nil, err
		}
		res.Target = client.Service
	}
	if to := r.URL.Query().Get("to"); to != "" {
		t, err := g.target(to)
		res.Target = t.service
		if err != nil {
			res.Error = err.Error()
		}
	}
	if id == nil {
		return res, nil
	}
	res.User, res.Via, res.Login = id.user, id.via, id.login
	if client != nil && id.user.HasAccess(client.Service) && id.user.Email == "" {
		res.Error = id.user.Name + " has no email, which signing in to " + client.Service + " needs. Ask an admin to add one"
	}
	for _, s := range g.routed() {
		if id.user.HasAccess(s.Name) {
			l := link{Name: s.Name}
			if host := g.hosts(s.Name); host != "" {
				l.URL = "https://" + host
			}
			res.Services = append(res.Services, l)
		}
	}
	return res, nil
}

func (g *gate) signIn(w http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
	var body struct{ Key string }
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	u, keyID, err := g.db.KeyUser(strings.TrimSpace(body.Key))
	if errors.Is(err, store.ErrNotFound) {
		return nil, &apiError{http.StatusUnauthorized, "That key isn't valid"}
	}
	if err != nil {
		return nil, err
	}
	if old, err := r.Cookie(cookieName); err == nil {
		if s, err := g.db.Session(old.Value, ""); err == nil {
			g.db.DeleteSession(s.ID)
		}
	}
	token, err := g.db.CreateSession(u.ID, keyID, "", "")
	if err != nil {
		return nil, err
	}
	setCookie(w, token)
	req := requestOf(r.Context())
	req.user, req.via = u.Name, "key"
	return nil, nil
}

func (g *gate) signOut(w http.ResponseWriter, r *http.Request, id *identity) (any, error) {
	clearCookie(w)
	if id != nil && id.via == "session" {
		return nil, g.db.DeleteSession(id.session)
	}
	return nil, nil
}

func (g *gate) setup(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
	var body struct{ Token, Name string }
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	gone := &apiError{http.StatusNotFound, "This setup link has expired or was already used"}
	if subtle.ConstantTimeCompare([]byte(body.Token), []byte(g.setupToken)) != 1 {
		return nil, gone
	}
	name, err := userName(body.Name)
	if err != nil {
		return nil, err
	}
	key, err := g.db.CreateFirstAdmin(name)
	if errors.Is(err, store.ErrNotEmpty) {
		return nil, gone
	}
	if err != nil {
		return nil, err
	}
	slog.Info("created the first admin", "user", name)
	return map[string]string{"key": key}, nil
}

// user names are what apps get as preferred_username and what reset-key takes,
// so they're limited to what every app accepts as a username
var validUserName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

func userName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if !validUserName.MatchString(name) {
		return "", badRequest("A name is up to 32 lowercase letters, digits, dots, dashes and underscores, like ezra or sylv.v")
	}
	return name, nil
}

func (g *gate) saveUser(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
	var u store.User
	if err := decode(r, &u); err != nil {
		return nil, err
	}
	u.ID = r.PathValue("id")
	name, err := userName(u.Name)
	if err != nil {
		return nil, err
	}
	u.Name = name
	userID, err := g.db.SaveUser(u)
	if errors.Is(err, store.ErrExists) {
		return nil, &apiError{http.StatusConflict, "There's already a user called " + u.Name}
	}
	return map[string]string{"id": userID}, err
}

func (g *gate) deleteUser(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
	return nil, g.db.DeleteUser(r.PathValue("id"))
}

func (g *gate) addKey(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
	var body struct{ Label string }
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	key, err := g.db.AddKey(r.PathValue("id"), strings.TrimSpace(body.Label))
	if err != nil {
		return nil, err
	}
	return map[string]string{"key": key}, nil
}

func (g *gate) resetKeys(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
	key, err := g.db.ResetKeys(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	return map[string]string{"key": key}, nil
}

func (g *gate) linkLogin(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
	var l store.Login
	if err := decode(r, &l); err != nil {
		return nil, err
	}
	err := g.db.LinkLogin(r.PathValue("id"), l)
	if errors.Is(err, store.ErrExists) {
		return nil, &apiError{http.StatusConflict, l.Name + " is already linked to a user"}
	}
	return nil, err
}

// tailnetLogins lists the logins of untagged devices the tailgate node can see, for linking
func (g *gate) tailnetLogins(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
	peers, err := g.peers(r.Context())
	if err != nil {
		return nil, err
	}
	logins := []store.Login{}
	for _, p := range peers {
		if len(p.Tags) == 0 && !slices.ContainsFunc(logins, func(l store.Login) bool { return l.ID == p.LoginID }) {
			logins = append(logins, store.Login{ID: p.LoginID, Name: p.Login})
		}
	}
	slices.SortFunc(logins, func(a, b store.Login) int { return strings.Compare(a.Name, b.Name) })
	return logins, nil
}

func (g *gate) adminServices(http.ResponseWriter, *http.Request, *identity) (any, error) {
	type nodeInfo struct {
		State       string `json:"state,omitempty"`
		URL         string `json:"url,omitempty"`
		SignCommand string `json:"signCommand,omitempty"`
		NodeError   string `json:"nodeError,omitempty"`
	}
	type svc struct {
		nodeInfo
		Name                   string `json:"name"`
		Container              string `json:"container"`
		Port                   int    `json:"port"`
		Funnel                 bool   `json:"funnel"`
		DangerouslyDisableAuth bool   `json:"dangerouslyDisableAuth"`
		// upstream is where requests are proxied, "" while the container isn't running
		Upstream string `json:"upstream,omitempty"`
		Error    string `json:"error,omitempty"`
	}
	nodeOf := func(st node.Status) nodeInfo {
		n := nodeInfo{State: string(st.State), SignCommand: st.SignCommand}
		if st.DNSName != "" {
			n.URL = "https://" + st.DNSName
		}
		if st.Err != nil {
			n.NodeError = st.Err.Error()
		}
		return n
	}
	services, unused, dockerErr := g.snapshot()
	res := struct {
		Tailgate nodeInfo `json:"tailgate"`
		Problems []string `json:"problems"`
		Services []svc    `json:"services"`
		Unused   []string `json:"unused"`
	}{Problems: []string{}, Services: []svc{}, Unused: append([]string{}, unused...)}
	if self := g.nodes.Self(); self != nil {
		res.Tailgate = nodeOf(self.Status())
	}
	if g.geo != nil {
		if p := g.geo.Problem(); p != "" {
			res.Problems = append(res.Problems, p)
		}
	}
	if dockerErr != nil {
		res.Problems = append(res.Problems, "Docker can't be read, so changes to containers aren't picked up: "+dockerErr.Error())
	}
	for _, s := range services {
		v := svc{Name: s.Name, Container: s.Container, Port: s.Port, Funnel: s.Funnel, DangerouslyDisableAuth: s.DangerouslyDisableAuth}
		if s.Addr != "" {
			v.Upstream = "http://" + net.JoinHostPort(s.Addr, strconv.Itoa(s.Port))
		}
		if s.Err != nil {
			v.Error = s.Err.Error()
		} else if st, ok := g.nodeStatus(s.Name); ok {
			v.nodeInfo = nodeOf(st)
		}
		res.Services = append(res.Services, v)
	}
	// errors first, then services anyone on the internet can use
	rank := func(s svc) int {
		switch {
		case s.Error != "":
			return 0
		case s.Funnel && s.DangerouslyDisableAuth:
			return 1
		}
		return 2
	}
	slices.SortStableFunc(res.Services, func(a, b svc) int { return rank(a) - rank(b) })
	return res, nil
}

func (g *gate) removeUnused(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
	// a container that got its labels meanwhile wasn't started, so look again
	defer g.refresh()
	// finish even if the admin closes the page, or nodes would be half removed
	removed, err := g.nodes.RemoveUnused(context.WithoutCancel(r.Context()), g.inUse())
	return map[string][]string{"removed": append([]string{}, removed...)}, err
}

type ruleList struct {
	Rules []rules.Rule `json:"rules"`
	// Error is why the saved rules can't be loaded
	Error string `json:"error,omitempty"`
	// Errors are the problems that stopped a save
	Errors []rules.Error `json:"errors,omitempty"`
}

func (g *gate) listRules(http.ResponseWriter, *http.Request, *identity) (any, error) {
	list, err := g.db.Rules()
	if err != nil {
		return nil, err
	}
	res := ruleList{Rules: list}
	if _, err := g.ruleSet(); err != nil {
		res.Error = err.Error()
	}
	return res, nil
}

// saveRules replaces every rule, but only when they all compile
func (g *gate) saveRules(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
	var body ruleList
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	for i := range body.Rules {
		if body.Rules[i].ID == "" {
			body.Rules[i].ID = rand.Text()
		}
	}
	set, errs := rules.Compile(body.Rules)
	if errs != nil {
		return ruleList{Rules: body.Rules, Errors: errs}, nil
	}
	if err := g.db.SaveRules(body.Rules); err != nil {
		return nil, err
	}
	g.mu.Lock()
	g.rules, g.rulesErr = set, nil
	g.mu.Unlock()
	slog.Info("saved the rules", "rules", len(body.Rules))
	return ruleList{Rules: body.Rules}, nil
}

// checkRule compiles one expression while the admin types
func checkRule(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
	var body struct {
		Section   string
		Expr      string
		Condition bool
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	return map[string]*rules.Error{"problem": rules.Check(body.Section, body.Expr, body.Condition)}, nil
}

func (g *gate) ruleSchema(http.ResponseWriter, *http.Request, *identity) (any, error) {
	services := []string{}
	for _, s := range g.routed() {
		services = append(services, s.Name)
	}
	return map[string]any{"values": rules.Schema(), "services": services}, nil
}

func (g *gate) oidcClients(http.ResponseWriter, *http.Request, *identity) (any, error) {
	list, err := g.db.OIDCClients()
	return map[string]any{"issuer": g.issuer(), "clients": list}, err
}

// saveOIDCClient creates or updates a client. Redirect URIs are https or an
// app's own scheme, so a typo can't send codes somewhere unsafe
func (g *gate) saveOIDCClient(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
	var c store.OIDCClient
	if err := decode(r, &c); err != nil {
		return nil, err
	}
	c.ID = r.PathValue("id")
	c.Name, c.Service = strings.TrimSpace(c.Name), strings.TrimSpace(c.Service)
	if c.Name == "" || c.Service == "" {
		return nil, badRequest("A name and a service are required")
	}
	if len(c.RedirectURIs) == 0 {
		return nil, badRequest("At least one redirect URI is required")
	}
	for _, uri := range c.RedirectURIs {
		u, err := url.Parse(uri)
		switch {
		case err != nil || u.Scheme == "":
			return nil, badRequest(uri + " isn't a full URL")
		case strings.Contains(uri, "#"):
			return nil, badRequest(uri + " can't have a #fragment")
		case slices.Contains([]string{"http", "javascript", "data", "file"}, u.Scheme):
			return nil, badRequest(uri + " has to be https or an app's own scheme")
		case u.Scheme == "https" && u.Host == "":
			return nil, badRequest(uri + " has no host")
		}
	}
	id, secret, err := g.db.SaveOIDCClient(c)
	return map[string]string{"id": id, "secret": secret}, err
}
