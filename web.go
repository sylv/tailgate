package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/sylv/tailgate/app"
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
	mux.Handle("GET /assets/", assets(ui))

	mux.HandleFunc("GET /api/me", g.api(false, g.me))
	mux.HandleFunc("GET /api/events", g.stream)
	mux.HandleFunc("POST /api/login", g.api(false, g.signIn))
	mux.HandleFunc("POST /api/logout", g.api(false, g.signOut))
	mux.HandleFunc("DELETE /api/keys/{id}", g.api(false, g.removeCred(funnel, false)))
	mux.HandleFunc("DELETE /api/passkeys/{id}", g.api(false, g.removeCred(funnel, true)))
	if g.passkeys {
		g.passkeyRoutes(mux)
	}
	g.oidc(mux)
	if funnel {
		for _, path := range []string{"/setup", "/admin", "/admin/"} {
			mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
				plain(w, r, http.StatusNotFound, "this page is only available on the tailnet")
			})
		}
	} else {
		g.tailnetOnly(mux)
	}

	// every app shares the tailnet's site, so SameSite cookies don't stop one
	// from posting here. this refuses cross origin writes to every route, while
	// apps swapping oidc codes send no origin and pass
	h := http.NewCrossOriginProtection().Handler(mux)
	return compressed(requests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Robots-Tag", noindex)
		if !strings.HasPrefix(r.URL.Path, "/assets/") {
			id, err := g.identify(w, r, "")
			if err != nil {
				slog.Error("reading credentials failed", "request_id", requestOf(r.Context()).id, "error", err.Error())
				plain(w, r, http.StatusInternalServerError, "sign in failed")
				return
			}
			requestOf(r.Context()).ident = id
		}
		if (funnel || r.URL.Path == tracePath) && !g.selfRules(w, r) {
			return
		}
		h.ServeHTTP(w, r)
	})))
}

// selfRules answers the trace and, over funnel, runs the pre-auth rules on
// tailgate's own pages like an app. the tailnet skips them so a bad rule
// can't lock admins out of the page that fixes it. It's false once the request
// is answered
func (g *gate) selfRules(w http.ResponseWriter, r *http.Request) bool {
	id := requestOf(r.Context()).ident
	hostname, _, _ := strings.Cut(g.selfHost(), ".")
	req, err := g.ruleRequest(r, app.App{Hostname: hostname, Funnel: true, Noindex: true})
	if err != nil {
		slog.Error("reading the request for rules failed", "request_id", requestOf(r.Context()).id, "error", err.Error())
		plain(w, r, http.StatusInternalServerError, "internal server error")
		return false
	}
	if r.URL.Path == tracePath {
		trace(w, r, req, id)
		return false
	}
	set, err := g.ruleSet()
	if err != nil {
		plain(w, r, http.StatusServiceUnavailable, "tailgate is unavailable")
		return false
	}
	res := requestOf(r.Context()).rules
	if ended(w, r, set.Run(rules.Pre, req, rules.User{SignedIn: id != nil}, res), res) {
		return false
	}
	res.ApplyResponse(w.Header())
	return true
}

func (g *gate) tailnetOnly(mux *http.ServeMux) {
	for _, path := range []string{"/setup", "/admin", "/admin/"} {
		mux.HandleFunc("GET "+path, g.page)
	}
	mux.HandleFunc("POST /api/setup", g.api(false, g.setup))
	mux.HandleFunc("GET /api/admin/users", g.api(true, func(_ http.ResponseWriter, _ *http.Request, id *identity) (any, error) {
		users, err := g.db.Users()
		for i := range users {
			markCreds(id, false, &users[i])
		}
		return users, err
	}))
	mux.HandleFunc("POST /api/admin/users", g.api(true, g.saveUser))
	mux.HandleFunc("PUT /api/admin/users/{id}", g.api(true, g.saveUser))
	mux.HandleFunc("DELETE /api/admin/users/{id}", g.api(true, g.deleteUser))
	mux.HandleFunc("POST /api/admin/users/{id}/keys", g.api(true, g.addKey))
	mux.HandleFunc("POST /api/admin/users/{id}/reset-keys", g.api(true, g.resetKeys))
	mux.HandleFunc("POST /api/admin/users/{id}/logins", g.api(true, g.linkLogin))
	mux.HandleFunc("DELETE /api/admin/logins/{id}", g.api(true, func(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			return nil, store.ErrNotFound
		}
		return nil, g.db.UnlinkLogin(id)
	}))
	mux.HandleFunc("GET /api/admin/logins", g.api(true, g.tailnetLogins))
	mux.HandleFunc("GET /api/admin/apps", g.api(true, g.adminApps))
	mux.HandleFunc("POST /api/admin/apps/{name}/login", g.api(true, func(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
		return nil, g.nodes.Login(r.Context(), r.PathValue("name"))
	}))
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
	if index() == nil {
		plain(w, r, http.StatusServiceUnavailable, "ui was not built into this binary")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(index())
}

// login skips the login page when the visitor is already signed in and can
// reach the app they came from
func (g *gate) login(w http.ResponseWriter, r *http.Request) {
	id := requestOf(r.Context()).ident
	to := r.URL.Query().Get("to")
	if id != nil && to == "" {
		redirect(w, "/")
		return
	}
	if id != nil {
		if t, err := g.target(to); err == nil && id.user.HasAccess(t.app) {
			redirect(w, g.continueURL(id, t, r.URL.Query().Get("state")))
			return
		}
	}
	g.page(w, r)
}

type target struct {
	app, host, path string
}

// target reads ?to=, an app name and a path. The URL is built from the
// app's own host so it can't point anywhere else
func (g *gate) target(to string) (target, error) {
	name, path, _ := strings.Cut(to, ":")
	_, ok := g.route(name)
	st, _ := g.appStatus(name)
	host := st.DNSName
	// a path starting with // or /\ is another host to a browser. one error for
	// everything, so funnel visitors can't use it to find out which apps exist
	if !ok || host == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, "/\\") {
		return target{}, errors.New("The link you followed is broken")
	}
	return target{app: name, host: host, path: path}, nil
}

// continueURL sends a signed in visitor on to the app with a code for an app session, made
// for the browser that holds state. Without state, like for a link someone sent, the app
// is opened instead, which sends the visitor back with this browser's own state
func (g *gate) continueURL(id *identity, t target, state string) string {
	if id.via == "tailnet" || state == "" {
		// the app signs a linked login in by itself
		return "https://" + t.host + t.path
	}
	c := g.codes.add(code{app: t.app, path: t.path, state: state, userID: id.user.ID, cred: id.cred, parent: id.session}, time.Minute)
	return "https://" + t.host + callbackPath + "?code=" + c
}

type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func badRequest(msg string) error { return &apiError{http.StatusBadRequest, msg} }

// api runs fn for a JSON endpoint. Admin endpoints need a key or passkey session, since a
// tailnet login is any device its owner left unlocked. Every admin change tells open pages
func (g *gate) api(admin bool, fn func(http.ResponseWriter, *http.Request, *identity) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		id := requestOf(r.Context()).ident
		if id != nil {
			// read before the handler runs, so the data is at least this new
			v, _ := g.events.current()
			w.Header().Set("X-Tailgate-Version", strconv.Itoa(v))
		}
		var err error
		switch {
		case admin && (id == nil || !id.user.Admin):
			err = &apiError{http.StatusForbidden, "Only admins can do that"}
		case admin && id.via != "session":
			err = &apiError{http.StatusForbidden, "Sign in with your key or a passkey to use the admin pages"}
		}
		var v any
		if err == nil {
			v, err = fn(w, r, id)
		}
		if err == nil && admin && r.Method != http.MethodGet {
			g.events.bump()
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
	// URL is "" when the visitor can't open it, and Why says why
	URL string `json:"url"`
	Why string `json:"why,omitempty"`
}

func (g *gate) me(_ http.ResponseWriter, r *http.Request, id *identity) (any, error) {
	res := struct {
		User   *store.User `json:"user"`
		Via    string      `json:"via,omitempty"`
		Login  string      `json:"login,omitempty"`
		Apps   []link      `json:"apps"`
		Target string      `json:"target,omitempty"`
		Error  string      `json:"error,omitempty"`
		// Keys and Passkeys are how the user signs in
		Keys     []store.Key     `json:"keys,omitempty"`
		Passkeys []store.Passkey `json:"passkeys,omitempty"`
		// PasskeysOn is TAILGATE_PASSKEYS
		PasskeysOn bool `json:"passkeysOn"`
		// CanAddPasskey is whether this session may add one, if the browser can
		CanAddPasskey bool `json:"canAddPasskey"`
		// Ingress and RequestID are for the page footer, so a visitor can say how they came and which request it was
		Ingress   string `json:"ingress"`
		RequestID string `json:"requestId"`
	}{Apps: []link{}, PasskeysOn: g.passkeys, Ingress: node.Ingress(r.Context()), RequestID: requestOf(r.Context()).id}
	// client is the oidc client the login page is signing in to
	var client *store.OIDCClient
	if clientID := r.URL.Query().Get("client"); clientID != "" {
		var err error
		if client, err = g.db.OIDCClient(clientID); err != nil {
			return nil, err
		}
		res.Target = g.hostname(client.App)
	}
	if to := r.URL.Query().Get("to"); to != "" {
		t, err := g.target(to)
		res.Target = g.hostname(t.app)
		if err != nil {
			res.Error = err.Error()
		}
	}
	if id == nil {
		return res, nil
	}
	res.User, res.Via, res.Login = id.user, id.via, id.login
	d, err := g.db.UserDetail(id.user.ID)
	if err != nil {
		return nil, err
	}
	markCreds(id, res.Ingress == "funnel", d)
	res.CanAddPasskey = g.passkeys && id.via == "session"
	res.Keys, res.Passkeys = d.Keys, d.Passkeys
	if client != nil && id.user.HasAccess(client.App) && id.user.Email == "" {
		res.Error = id.user.Name + " has no email, which signing in to " + g.hostname(client.App) + " needs. Ask an admin to add one"
	}
	for _, s := range g.routed() {
		if id.user.HasAccess(s.ID) {
			l := link{Name: s.Hostname, Why: "Starting"}
			// a node that couldn't turn funnel on still serves the tailnet
			if st, ok := g.appStatus(s.ID); ok && (st.State == node.Online || st.State == node.FunnelNotAllowed) {
				l.URL, l.Why = "https://"+st.DNSName, ""
				// funnel visitors can't reach a tailnet only app, the link would time out
				if res.Ingress == "funnel" && !st.Funnel {
					l.URL, l.Why = "", "Tailnet only"
				}
			}
			res.Apps = append(res.Apps, l)
		}
	}
	return res, nil
}

// removeErr is why id can't remove a key or passkey of owner, nil when it can. users
// remove their own, and admins anyone's on the tailnet with the same sign in the admin
// pages need. nobody removes the one they're signed in with or their own last key, so
// they can't lock themselves out
func removeErr(id *identity, funnel bool, owner *store.UserDetail, target string, passkey bool) error {
	own := owner.ID == id.user.ID
	switch {
	case !own && (!id.user.Admin || id.via != "session" || funnel):
		return store.ErrNotFound
	case target == id.cred.KeyID || target == id.cred.PasskeyID:
		return &apiError{http.StatusBadRequest, "You're signed in with that one, sign in another way to remove it"}
	case own && !passkey && len(owner.Keys) == 1:
		return &apiError{http.StatusBadRequest, "That's your last key, ask an admin for a new one first"}
	}
	return nil
}

// markCreds sets which of d's keys and passkeys id signed in with and can remove
func markCreds(id *identity, funnel bool, d *store.UserDetail) {
	for i := range d.Keys {
		k := &d.Keys[i]
		k.Current, k.Removable = k.ID == id.cred.KeyID, removeErr(id, funnel, d, k.ID, false) == nil
	}
	for i := range d.Passkeys {
		p := &d.Passkeys[i]
		p.Current, p.Removable = p.ID == id.cred.PasskeyID, removeErr(id, funnel, d, p.ID, true) == nil
	}
}

func (g *gate) removeCred(funnel, passkey bool) func(http.ResponseWriter, *http.Request, *identity) (any, error) {
	remove := g.db.RevokeKey
	if passkey {
		remove = g.db.DeletePasskey
	}
	return func(_ http.ResponseWriter, r *http.Request, id *identity) (any, error) {
		if id == nil {
			return nil, &apiError{http.StatusUnauthorized, "Sign in first"}
		}
		target := r.PathValue("id")
		owner, err := g.db.CredOwner(target, passkey)
		if err != nil {
			return nil, err
		}
		d, err := g.db.UserDetail(owner)
		if err != nil {
			return nil, err
		}
		if err := removeErr(id, funnel, d, target, passkey); err != nil {
			return nil, err
		}
		if err := remove(target); err != nil {
			return nil, err
		}
		g.events.bump()
		return nil, nil
	}
}

func (g *gate) signIn(w http.ResponseWriter, r *http.Request, id *identity) (any, error) {
	var body struct{ Key string }
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(body.Key)
	if strings.HasPrefix(key, "tgs_") {
		return nil, badRequest("That's a setup key. Open the setup link from tailgate's logs instead")
	}
	u, keyID, err := g.db.KeyUser(key)
	if errors.Is(err, store.ErrNotFound) {
		return nil, &apiError{http.StatusUnauthorized, "That key isn't valid"}
	}
	if err != nil {
		return nil, err
	}
	if err := g.startSession(w, r, id, u, store.Cred{KeyID: keyID}); err != nil {
		return nil, err
	}
	// the page asks only when the device has a built in authenticator, which it can tell and we can't
	passkeys, err := g.db.Passkeys(u.ID)
	return map[string]bool{"offerPasskey": g.passkeys && !u.SkipPasskeyPrompt && len(passkeys) == 0}, err
}

// startSession signs the visitor in as u, replacing the session they had
func (g *gate) startSession(w http.ResponseWriter, r *http.Request, old *identity, u *store.User, c store.Cred) error {
	if old != nil && old.via == "session" {
		if err := g.db.DeleteSession(old.session); err != nil {
			return err
		}
	}
	token, err := g.db.CreateSession(u.ID, c, "", "")
	if err != nil {
		return err
	}
	setCookie(w, token, time.Now().Add(store.SessionLifetime))
	req := requestOf(r.Context())
	req.user, req.via = u.Name, "session"
	return nil
}

func (g *gate) signOut(w http.ResponseWriter, r *http.Request, id *identity) (any, error) {
	clearCookie(w, cookieName)
	if id != nil && id.via == "session" {
		return nil, g.db.DeleteSession(id.session)
	}
	return nil, nil
}

func (g *gate) setup(_ http.ResponseWriter, r *http.Request, _ *identity) (any, error) {
	var body struct{ Key, Name string }
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	name, err := userName(body.Name)
	if err != nil {
		return nil, err
	}
	key, err := g.db.FinishSetup(body.Key, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, &apiError{http.StatusNotFound, "This setup link has expired or was already used"}
	}
	if err != nil {
		return nil, err
	}
	slog.Info("setup is done, starting apps", "admin", name)
	g.refresh()
	g.events.bump()
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

func (g *gate) adminApps(http.ResponseWriter, *http.Request, *identity) (any, error) {
	type nodeInfo struct {
		State string `json:"state,omitempty"`
		// Funnel is whether funnel is really on, not just asked for
		Funnel      bool   `json:"funnelOn,omitempty"`
		URL         string `json:"url,omitempty"`
		SignCommand string `json:"signCommand,omitempty"`
		NodeError   string `json:"nodeError,omitempty"`
		// RetryAt is when a cert is tried again, in unix seconds
		RetryAt int64 `json:"retryAt,omitempty"`
		// TailscaleService is set for a tailscale service on the tailgate node, instead of its own node
		TailscaleService bool `json:"tailscaleService,omitempty"`
	}
	type svc struct {
		nodeInfo
		ID                     string `json:"id"`
		Hostname               string `json:"hostname"`
		Container              string `json:"container"`
		Port                   int    `json:"port"`
		Funnel                 bool   `json:"funnel"`
		DangerouslyDisableAuth bool   `json:"dangerouslyDisableAuth"`
		// Address is where requests are proxied, "" while the container isn't running
		Address string `json:"address,omitempty"`
		Error   string `json:"error,omitempty"`
	}
	nodeOf := func(st node.Status) nodeInfo {
		n := nodeInfo{State: string(st.State), Funnel: st.Funnel, SignCommand: st.SignCommand, TailscaleService: st.Service}
		if st.DNSName != "" {
			n.URL = "https://" + st.DNSName
		}
		if st.Err != nil {
			n.NodeError = st.Err.Error()
		}
		if !st.RetryAt.IsZero() {
			n.RetryAt = st.RetryAt.Unix()
		}
		return n
	}
	apps, unused, dockerErr := g.snapshot()
	res := struct {
		Tailgate nodeInfo `json:"tailgate"`
		Problems []string `json:"problems"`
		Apps     []svc    `json:"apps"`
		Unused   []string `json:"unused"`
	}{Problems: []string{}, Apps: []svc{}, Unused: append([]string{}, unused...)}
	if self := g.nodes.Self(); self != nil {
		res.Tailgate = nodeOf(self.Status())
	}
	if p := g.nodes.Problem(); p != "" {
		res.Problems = append(res.Problems, p)
	}
	if dockerErr != nil {
		res.Problems = append(res.Problems, "Docker can't be read, so changes to containers aren't picked up: "+dockerErr.Error())
	}
	for _, s := range apps {
		v := svc{ID: s.ID, Hostname: s.Hostname, Container: s.Container, Port: s.Port, Funnel: s.Funnel, DangerouslyDisableAuth: s.DangerouslyDisableAuth}
		if s.Addr != "" {
			v.Address = "http://" + net.JoinHostPort(s.Addr, strconv.Itoa(s.Port))
		}
		if s.Err != nil {
			v.Error = s.Err.Error()
		} else if st, ok := g.nodes.Status(s.ID); ok {
			v.nodeInfo = nodeOf(st)
		}
		res.Apps = append(res.Apps, v)
	}
	// errors first, then apps anyone on the internet can use
	rank := func(s svc) int {
		switch {
		case s.Error != "":
			return 0
		case s.Funnel && s.DangerouslyDisableAuth:
			return 1
		}
		return 2
	}
	slices.SortStableFunc(res.Apps, func(a, b svc) int { return rank(a) - rank(b) })
	return res, nil
}

type ruleList struct {
	Rules []rules.Rule `json:"rules"`
	// Version is a hash of the saved rules, so a save from a page that loaded older ones is refused
	Version string `json:"version"`
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
	res := ruleList{Rules: list, Version: rulesVersion(list)}
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
		return ruleList{Rules: body.Rules, Version: body.Version, Errors: errs}, nil
	}
	// one lock around reading and saving, so two saves can't both pass the version check
	g.saving.Lock()
	defer g.saving.Unlock()
	saved, err := g.db.Rules()
	if err != nil {
		return nil, err
	}
	if rulesVersion(saved) != body.Version {
		return nil, &apiError{http.StatusConflict, "The rules were changed somewhere else since this page loaded them. Copy your changes and reload"}
	}
	if err := g.db.SaveRules(body.Rules); err != nil {
		return nil, err
	}
	g.mu.Lock()
	g.rules, g.rulesErr = set, nil
	g.mu.Unlock()
	slog.Info("saved the rules", "rules", len(body.Rules))
	return ruleList{Rules: body.Rules, Version: rulesVersion(body.Rules)}, nil
}

func rulesVersion(list []rules.Rule) string {
	b, _ := json.Marshal(append([]rules.Rule{}, list...))
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
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
	ids, hostnames := []string{}, []string{}
	for _, s := range g.routed() {
		ids, hostnames = append(ids, s.ID), append(hostnames, s.Hostname)
	}
	return map[string]any{"values": rules.Schema(), "apps": ids, "hostnames": hostnames}, nil
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
	c.Name, c.App = strings.TrimSpace(c.Name), strings.TrimSpace(c.App)
	if c.Name == "" || c.App == "" {
		return nil, badRequest("A name and an app are required")
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
