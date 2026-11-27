package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/sylv/tailgate/app"
	"github.com/sylv/tailgate/node"
	"github.com/sylv/tailgate/rules"
	"github.com/sylv/tailgate/store"
)

type testGate struct {
	*gate
	web, funnel, photos, open http.Handler
	// got is the last request the app received
	got  *http.Request
	peer *node.Peer
	key  string
}

func newTestGate(t *testing.T) *testGate {
	db, err := store.Open(filepath.Join(t.TempDir(), "tailgate.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	tg := &testGate{gate: newGate(db)}
	tg.signer = testKey()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { tg.got = r }))
	t.Cleanup(upstream.Close)
	addr, port, _ := strings.Cut(strings.TrimPrefix(upstream.URL, "http://"), ":")
	p, _ := strconv.Atoi(port)
	tg.routes = map[string]app.App{
		"photos": {ID: "photos", Hostname: "photos", Addr: addr, Port: p},
		"open":   {ID: "open", Hostname: "open", Addr: addr, Port: p, DangerouslyDisableAuth: true},
		"lyra":   {ID: "lyra", Hostname: "lyra"},
	}
	tg.selfHost = func() string { return "tailgate.ts.net" }
	tg.appStatus = func(id string) (node.Status, bool) {
		host, ok := map[string]string{"photos": "photos.ts.net", "open": "open.ts.net"}[id]
		return node.Status{State: node.Online, DNSName: host}, ok
	}
	tg.gate.peer = func(context.Context) (*node.Peer, error) { return tg.peer, nil }
	tg.web, tg.funnel, tg.photos, tg.open = tg.gate.web(false), tg.gate.web(true), tg.appHandler("photos"), tg.appHandler("open")
	id, _ := db.SaveUser(store.User{Name: "ezra", Email: "ezra@example.com", Apps: []string{"photos"}})
	tg.key, _ = db.AddKey(id, "")
	return tg
}

func do(h http.Handler, method, target string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader("{}"))
	req.RemoteAddr = "100.64.0.7:51234"
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func cookie(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			return c.Value
		}
	}
	return ""
}

func page() http.Header { return http.Header{"Sec-Fetch-Mode": {"navigate"}} }

var testKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	return key
})

// signIn goes from an app's page through the login page to its callback, like a browser
// signed in on the tailgate node, and returns the callback url and the app's state cookie
func (tg *testGate) signIn(t *testing.T, path string, signedIn http.Header) (*url.URL, http.Header) {
	t.Helper()
	rec := do(tg.photos, "GET", "https://photos.ts.net"+path, page())
	var state string
	for _, c := range rec.Result().Cookies() {
		if c.Name == stateCookie {
			state = c.Value
		}
	}
	login, _ := url.Parse(rec.Header().Get("Location"))
	rec = do(tg.web, "GET", "https://tailgate.ts.net"+login.RequestURI(), signedIn)
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || loc.Host != "photos.ts.net" || loc.Path != callbackPath || state == "" {
		t.Fatalf("login got %d %q", rec.Code, loc)
	}
	return loc, http.Header{"Cookie": {stateCookie + "=" + state}, "Sec-Fetch-Mode": {"navigate"}}
}

func TestSignedOut(t *testing.T) {
	tg := newTestGate(t)
	rec := do(tg.photos, "GET", "https://photos.ts.net/albums/1?sort=new", page())
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || !strings.HasPrefix(loc, "https://tailgate.ts.net/login?state=") || !strings.HasSuffix(loc, "&to=photos%3A%2Falbums%2F1%3Fsort%3Dnew") {
		t.Fatalf("page navigation got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = do(tg.photos, "GET", "https://photos.ts.net/api/albums", nil)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("api request got %d %q", rec.Code, rec.Body)
	}
	if tg.got != nil {
		t.Fatal("a signed out request reached the app")
	}
	// an unknown callback code looks like any other unknown path
	if rec := do(tg.photos, "GET", "https://photos.ts.net/.tailgate/callback?code=nope", page()); rec.Code != http.StatusNotFound {
		t.Fatalf("bad code got %d", rec.Code)
	}
}

func TestPageRequestID(t *testing.T) {
	tg := newTestGate(t)
	// ci doesn't build the ui, so the test brings its own page
	built := index
	index = func() []byte { return []byte("<html><head></head></html>") }
	t.Cleanup(func() { index = built })
	rec := do(tg.web, "GET", "https://tailgate.ts.net/", page())
	if id := rec.Header().Get(requestIDHeader); id == "" || !strings.Contains(rec.Body.String(), `"request":"`+id+`"`) {
		t.Fatalf("page doesn't name its request %q: %s", id, rec.Body)
	}
}

func TestLoginFlow(t *testing.T) {
	tg := newTestGate(t)
	api := http.Header{"Origin": {"https://tailgate.ts.net"}, "Content-Type": {"application/json"}}

	// writes from anywhere but tailgate's own pages are refused, including from an app on the same site
	if rec := do(tg.web, "POST", "https://tailgate.ts.net/api/login", http.Header{"Sec-Fetch-Site": {"same-site"}, "Origin": {"https://photos.ts.net"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("cross origin write got %d", rec.Code)
	}
	req := httptest.NewRequest("POST", "https://tailgate.ts.net/api/login", strings.NewReader(`{"key":"`+tg.key+`"}`))
	req.Header = api.Clone()
	rec := httptest.NewRecorder()
	tg.web.ServeHTTP(rec, req)
	session := cookie(rec)
	if rec.Code != http.StatusOK || session == "" {
		t.Fatalf("sign in got %d %s", rec.Code, rec.Body)
	}
	signedIn := http.Header{"Cookie": {cookieName + "=" + session}}

	// a path that's another host to the browser is refused
	if rec := do(tg.web, "GET", "https://tailgate.ts.net/login?to="+url.QueryEscape("photos://evil.example"), signedIn); rec.Code == http.StatusFound {
		t.Fatalf("open redirect to %q", rec.Header().Get("Location"))
	}

	// a link to the login page that didn't come from the app goes through the app for a state cookie
	rec = do(tg.web, "GET", "https://tailgate.ts.net/login?to="+url.QueryEscape("photos:/albums?x=1"), signedIn)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || loc != "https://photos.ts.net/albums?x=1" {
		t.Fatalf("login without state got %d %q", rec.Code, loc)
	}

	loc, state := tg.signIn(t, "/albums?x=1", signedIn)
	// a code only works on the host it was made for, and only once
	if rec := do(tg.appHandler("lyra"), "GET", "https://lyra.ts.net"+loc.RequestURI(), state); rec.Code != http.StatusNotFound {
		t.Fatalf("code on another host got %d", rec.Code)
	}
	loc, state = tg.signIn(t, "/albums?x=1", signedIn)
	// someone else's browser doesn't get a session from the link, it's sent back to sign in itself
	if rec := do(tg.photos, "GET", "https://photos.ts.net"+loc.RequestURI(), http.Header{"Cookie": {stateCookie + "=other"}}); cookie(rec) != "" || rec.Header().Get("Location") != "/albums?x=1" {
		t.Fatalf("callback in another browser got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	loc, state = tg.signIn(t, "/albums?x=1", signedIn)
	rec = do(tg.photos, "GET", "https://photos.ts.net"+loc.RequestURI(), state)
	app := cookie(rec)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/albums?x=1" || app == "" {
		t.Fatalf("callback got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := do(tg.photos, "GET", "https://photos.ts.net"+loc.RequestURI(), state); rec.Code != http.StatusNotFound {
		t.Fatalf("reused code got %d", rec.Code)
	}

	// the tailgate node's session doesn't work on the app, the app's own one does
	if rec := do(tg.photos, "GET", "https://photos.ts.net/", signedIn); rec.Code != http.StatusUnauthorized {
		t.Fatalf("tailgate cookie on photos got %d", rec.Code)
	}
	if rec := do(tg.photos, "GET", "https://photos.ts.net/", http.Header{"Cookie": {"theme=dark; " + cookieName + "=" + app}}); rec.Code != http.StatusOK {
		t.Fatalf("signed in got %d", rec.Code)
	}
	if c := tg.got.Header.Get("Cookie"); c != "theme=dark" {
		t.Fatalf("app got cookies %q", c)
	}

	// no code for an app the user can't reach
	if rec := do(tg.web, "GET", "https://tailgate.ts.net/login?to="+url.QueryEscape("lyra:/"), signedIn); rec.Code == http.StatusFound {
		t.Fatalf("got a code for lyra: %q", rec.Header().Get("Location"))
	}

	// signing out on the tailgate node ends the app session too
	h := api.Clone()
	h.Set("Cookie", cookieName+"="+session)
	if rec := do(tg.web, "POST", "https://tailgate.ts.net/api/logout", h); rec.Code != http.StatusOK {
		t.Fatalf("sign out got %d", rec.Code)
	}
	if rec := do(tg.photos, "GET", "https://photos.ts.net/", http.Header{"Cookie": {cookieName + "=" + app}}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("after sign out got %d", rec.Code)
	}
}

func TestKeys(t *testing.T) {
	tg := newTestGate(t)
	// a credential without the tg_ prefix is the app's and stays
	for _, c := range []struct {
		header   http.Header
		wantAuth string
	}{
		{http.Header{"Authorization": {"Bearer " + tg.key}}, ""},
		{http.Header{"Authorization": {"Bearer  " + tg.key + " "}}, ""},
		{http.Header{keyHeader: {tg.key}, "Authorization": {"Basic YXBwOmFwcA=="}}, "Basic YXBwOmFwcA=="},
	} {
		tg.got = nil
		if rec := do(tg.photos, "GET", "https://photos.ts.net/x?a=1", c.header); rec.Code != http.StatusOK || tg.got == nil {
			t.Fatalf("%v got %d", c.header, rec.Code)
		}
		if tg.got.Header.Get("Authorization") != c.wantAuth || tg.got.Header.Get(keyHeader) != "" {
			t.Errorf("%v: app got %v", c.header, tg.got.Header)
		}
	}

	if rec := do(tg.photos, "GET", "https://photos.ts.net/", http.Header{"Authorization": {"Bearer tg_wrong"}}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key got %d", rec.Code)
	}
	if rec := do(tg.appHandler("lyra"), "GET", "https://lyra.ts.net/", http.Header{keyHeader: {tg.key}}); rec.Code != http.StatusForbidden {
		t.Fatalf("no access got %d", rec.Code)
	}
}

func TestLinkedLogin(t *testing.T) {
	tg := newTestGate(t)
	id, _ := tg.db.UserIDByName("ezra")
	tg.db.LinkLogin(id, store.Login{ID: 7, Name: "ezra@"})
	tg.peer = &node.Peer{LoginID: 7, Login: "ezra@"}
	if rec := do(tg.photos, "GET", "https://photos.ts.net/", nil); rec.Code != http.StatusOK {
		t.Fatalf("linked login got %d", rec.Code)
	}
	// an admin signed in by their tailnet login has to sign in with a key for the admin pages
	tg.db.SetAdmin(id, true)
	if rec := do(tg.web, "GET", "https://tailgate.ts.net/api/admin/users", nil); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "key") {
		t.Fatalf("tailnet admin got %d %s", rec.Code, rec.Body)
	}
	tg.peer = &node.Peer{LoginID: 7, Login: "ezra@", Tags: []string{"tag:server"}}
	if rec := do(tg.photos, "GET", "https://photos.ts.net/", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("tagged device got %d", rec.Code)
	}
	tg.peer = &node.Peer{LoginID: 7, Login: "ezra@", Routes: true}
	if rec := do(tg.photos, "GET", "https://photos.ts.net/", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("subnet router got %d", rec.Code)
	}
	// a failed lookup never falls back to signed out
	tg.gate.peer = func(context.Context) (*node.Peer, error) { return nil, errors.New("whois timed out") }
	for _, h := range []http.Handler{tg.photos, tg.open} {
		if rec := do(h, "GET", "https://photos.ts.net/", nil); rec.Code != http.StatusInternalServerError {
			t.Fatalf("failed whois got %d", rec.Code)
		}
	}
}

func TestFunnelHasNoAdmin(t *testing.T) {
	tg := newTestGate(t)
	key := http.Header{"Cookie": {cookieName + "=nope"}}
	for _, path := range []string{"/admin", "/admin/users", "/setup", "/api/admin/users"} {
		if rec := do(tg.funnel, "GET", "https://tailgate.ts.net"+path, key); rec.Code != http.StatusNotFound {
			t.Errorf("%s on funnel got %d", path, rec.Code)
		}
		if rec := do(tg.web, "GET", "https://tailgate.ts.net"+path, key); rec.Code == http.StatusNotFound {
			t.Errorf("%s on the tailnet got 404", path)
		}
	}
	if rec := do(tg.funnel, "GET", "https://tailgate.ts.net/login?to=photos:/", nil); rec.Code == http.StatusNotFound {
		t.Errorf("login on funnel got %d", rec.Code)
	}
}

func TestProxyHeaders(t *testing.T) {
	tg := newTestGate(t)
	rec := do(tg.open, "GET", "https://open.ts.net/albums", http.Header{
		"X-Forwarded-For":  {"1.2.3.4"},
		"X-Real-Ip":        {"1.2.3.4"},
		"X-Forwarded-Host": {"evil.example"},
		requestIDHeader:    {"fake"},
		"Cookie":           {cookieName + "=whatever"},
	})
	if rec.Code != http.StatusOK || tg.got == nil {
		t.Fatalf("got %d", rec.Code)
	}
	for header, want := range map[string]string{
		"X-Forwarded-For":   "100.64.0.7",
		"X-Real-IP":         "100.64.0.7",
		"X-Forwarded-Host":  "open.ts.net",
		"X-Forwarded-Proto": "https",
		requestIDHeader:     rec.Header().Get(requestIDHeader),
		"Cookie":            "",
	} {
		if v := tg.got.Header.Get(header); v != want {
			t.Errorf("%s: got %q, want %q", header, v, want)
		}
	}
	if tg.got.Host != "open.ts.net" || tg.got.URL.Path != "/albums" {
		t.Errorf("got host %q path %q", tg.got.Host, tg.got.URL.Path)
	}

	// search engines are asked to skip tailgate's pages, and apps unless they opt out
	if v := rec.Header().Get("X-Robots-Tag"); v != "" {
		t.Errorf("noindex=false app got X-Robots-Tag %q", v)
	}
	s := tg.routes["open"]
	s.Noindex = true
	tg.routes["open"] = s
	for _, rec := range []*httptest.ResponseRecorder{do(tg.open, "GET", "https://open.ts.net/", nil), do(tg.funnel, "GET", "https://tailgate.ts.net/login", nil), do(tg.photos, "GET", "https://photos.ts.net/", nil)} {
		if v := rec.Header().Get("X-Robots-Tag"); v != noindex {
			t.Errorf("got X-Robots-Tag %q", v)
		}
	}

	tg.routes["open"] = app.App{ID: "open", Hostname: "open", DangerouslyDisableAuth: true}
	for _, h := range []http.Handler{tg.open, tg.appHandler("unknown")} {
		if rec := do(h, "GET", "https://open.ts.net/", nil); rec.Code != http.StatusBadGateway || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("got %d", rec.Code)
		}
	}
}

func TestRules(t *testing.T) {
	tg := newTestGate(t)
	tg.db.SaveRules([]rules.Rule{
		{ID: "1", Section: rules.Pre, Name: "no mcp", Condition: `path startsWith "/mcp"`, Effects: []rules.Effect{{Type: rules.Block, Value: `"No MCP"`}}, Mode: "on"},
		{ID: "2", Section: rules.Post, Name: "who", Condition: `true`, Effects: []rules.Effect{
			{Type: rules.SetRequestHeader, Name: "X-User", Value: `user.name`},
			{Type: rules.SetResponseHeader, Name: "X-Via", Value: `user.via`},
			{Type: rules.SetRequestHeader, Name: "X-Copy", Value: `header("X-Tailgate-Key") + header("Cookie") + cookie("__Host-tailgate_session")`},
			{Type: rules.SetRequestHeader, Name: "Authorization", Value: `"Bearer tg_planted"`},
			{Type: rules.SetRequestHeader, Name: "X-Login", Value: `tailnet.login`},
		}, Mode: "on"},
	})
	tg.loadRules()

	// a pre-auth block keeps a signed out visitor away from the login page
	rec := do(tg.photos, "GET", "https://photos.ts.net/mcp", page())
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "No MCP") || tg.got != nil {
		t.Fatalf("got %d %q", rec.Code, rec.Body)
	}
	// tailgate's own pages run pre-auth rules over funnel, but never on the tailnet
	if rec := do(tg.funnel, "GET", "https://tailgate.ts.net/mcp", nil); rec.Code != http.StatusForbidden {
		t.Errorf("funnel got %d", rec.Code)
	}
	if rec := do(tg.web, "GET", "https://tailgate.ts.net/mcp", nil); rec.Code == http.StatusForbidden {
		t.Errorf("tailnet got %d", rec.Code)
	}
	for _, h := range []http.Handler{tg.photos, tg.funnel, tg.web} {
		if rec := do(h, "GET", "https://photos.ts.net"+tracePath, http.Header{keyHeader: {tg.key}}); !strings.Contains(rec.Body.String(), "ip=100.64.0.7\n") {
			t.Errorf("trace got %d %q", rec.Code, rec.Body)
		}
	}
	if rec := do(tg.photos, "GET", "https://photos.ts.net"+tracePath, http.Header{keyHeader: {tg.key}}); !strings.Contains(rec.Body.String(), "auth=key\n") {
		t.Errorf("trace got %q", rec.Body)
	}

	rec = do(tg.photos, "GET", "https://photos.ts.net/", http.Header{keyHeader: {tg.key}, "X-User": {"admin"}, "Cookie": {cookieName + "=nope"}})
	if rec.Code != http.StatusOK || tg.got.Header.Get("X-User") != "ezra" || rec.Header().Get("X-Via") != "key" {
		t.Fatalf("got %d, X-User %q, X-Via %q", rec.Code, tg.got.Header.Get("X-User"), rec.Header().Get("X-Via"))
	}
	// rules can't read tailgate's credentials or send one to the app
	if v := tg.got.Header.Get("X-Copy") + tg.got.Header.Get("Authorization"); v != "" {
		t.Errorf("app got credentials %q", v)
	}
	// a subnet router speaks for its whole lan, so rules don't get its owner either
	tg.peer = &node.Peer{LoginID: 7, Login: "ezra@", Routes: true}
	if do(tg.open, "GET", "https://open.ts.net/", nil); tg.got.Header.Get("X-Login") != "" {
		t.Errorf("subnet router got login %q", tg.got.Header.Get("X-Login"))
	}
	tg.peer = nil

	// saved rules that don't compile block every app request
	tg.db.SaveRules([]rules.Rule{{ID: "1", Section: rules.Pre, Name: "broken", Condition: `nope`, Effects: []rules.Effect{{Type: rules.Block}}, Mode: "on"}})
	tg.loadRules()
	for _, h := range []http.Handler{tg.photos, tg.open} {
		if rec := do(h, "GET", "https://photos.ts.net/", http.Header{keyHeader: {tg.key}}); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("got %d with broken rules", rec.Code)
		}
	}
}

func TestSaveRules(t *testing.T) {
	tg := newTestGate(t)
	id, _ := tg.db.UserIDByName("ezra")
	tg.db.SetAdmin(id, true)
	_, keyID, _ := tg.db.KeyUser(tg.key)
	session, _ := tg.db.CreateSession(id, store.Cred{KeyID: keyID}, "", "")
	save := func(body string, status int) ruleList {
		req := httptest.NewRequest("PUT", "https://tailgate.ts.net/api/admin/rules", strings.NewReader(body))
		req.Header = http.Header{"Origin": {"https://tailgate.ts.net"}, "Content-Type": {"application/json"}, "Cookie": {cookieName + "=" + session}}
		rec := httptest.NewRecorder()
		tg.web.ServeHTTP(rec, req)
		var res ruleList
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || rec.Code != status {
			t.Fatalf("got %d %s", rec.Code, rec.Body)
		}
		return res
	}
	empty := rulesVersion(nil)
	res := save(`{"version":"`+empty+`","rules":[{"section":"pre","name":"x","condition":"user.name == \"ezra\"","effects":[{"type":"block"}],"mode":"on"}]}`, http.StatusOK)
	if len(res.Errors) != 1 || res.Errors[0].Field != "condition" {
		t.Fatalf("got %+v", res)
	}
	if list, _ := tg.db.Rules(); len(list) != 0 {
		t.Fatal("a rule that doesn't compile was saved")
	}
	res = save(`{"version":"`+empty+`","rules":[{"section":"pre","name":"x","condition":"path == \"/x\"","effects":[{"type":"block"}],"mode":"on"}]}`, http.StatusOK)
	if len(res.Errors) != 0 || res.Rules[0].ID == "" {
		t.Fatalf("got %+v", res)
	}
	if rec := do(tg.open, "GET", "https://open.ts.net/x", nil); rec.Code != http.StatusForbidden {
		t.Errorf("a saved rule didn't apply, got %d", rec.Code)
	}
	// a page that loaded the rules before that save can't overwrite it
	save(`{"version":"`+empty+`","rules":[]}`, http.StatusConflict)
	if list, _ := tg.db.Rules(); len(list) != 1 {
		t.Fatal("a stale save replaced the rules")
	}
}

func TestUserName(t *testing.T) {
	for name, ok := range map[string]bool{"ezra": true, " sylv.v ": true, "a_b-2": true, "Ezra": false, "": false, ".ezra": false, "ezra smith": false, "ezra@home": false, strings.Repeat("a", 33): false} {
		if _, err := userName(name); (err == nil) != ok {
			t.Errorf("%q: got %v", name, err)
		}
	}
}

func TestRemoveKey(t *testing.T) {
	tg := newTestGate(t)
	other, _ := tg.db.SaveUser(store.User{Name: "lyra"})
	tg.db.AddKey(other, "")
	theirs, _ := tg.db.UserDetail(other)
	id, _ := tg.db.UserIDByName("ezra")
	tg.db.AddKey(id, "spare")
	mine, _ := tg.db.UserDetail(id)
	var current, spare string
	for _, k := range mine.Keys {
		if k.Label == "spare" {
			spare = k.ID
		} else {
			current = k.ID
		}
	}

	req := httptest.NewRequest("POST", "https://tailgate.ts.net/api/login", strings.NewReader(`{"key":"`+tg.key+`"}`))
	req.Header = http.Header{"Origin": {"https://tailgate.ts.net"}, "Content-Type": {"application/json"}}
	rec := httptest.NewRecorder()
	tg.web.ServeHTTP(rec, req)
	signedIn := http.Header{"Cookie": {cookieName + "=" + cookie(rec)}, "Origin": {"https://tailgate.ts.net"}}
	del := func(h http.Handler, key string, header http.Header) int {
		return do(h, "DELETE", "https://tailgate.ts.net/api/keys/"+key, header).Code
	}

	if code := del(tg.web, theirs.Keys[0].ID, signedIn); code != http.StatusNotFound {
		t.Fatalf("someone else's key got %d", code)
	}
	if code := del(tg.web, current, signedIn); code != http.StatusBadRequest {
		t.Fatalf("the key this session signed in with got %d", code)
	}
	if code := del(tg.web, spare, signedIn); code != http.StatusOK {
		t.Fatalf("own key got %d", code)
	}
	if d, _ := tg.db.UserDetail(id); len(d.Keys) != 1 {
		t.Fatal("own key wasn't removed")
	}
	if code := del(tg.web, current, http.Header{"Origin": {"https://tailgate.ts.net"}}); code != http.StatusUnauthorized {
		t.Fatalf("signed out got %d", code)
	}
	// a tailnet login has no current key, but still can't remove the last one
	tg.db.LinkLogin(id, store.Login{ID: 7, Name: "ezra@"})
	tg.peer = &node.Peer{LoginID: 7, Login: "ezra@"}
	if code := del(tg.web, current, http.Header{"Origin": {"https://tailgate.ts.net"}}); code != http.StatusBadRequest {
		t.Fatalf("last key over a tailnet login got %d", code)
	}
	tg.peer = nil

	// admins remove anyone's, but only on the tailnet like the rest of the admin pages
	tg.db.SetAdmin(id, true)
	if code := del(tg.funnel, theirs.Keys[0].ID, signedIn); code != http.StatusNotFound {
		t.Fatalf("admin over funnel got %d", code)
	}
	if code := del(tg.web, theirs.Keys[0].ID, signedIn); code != http.StatusOK {
		t.Fatalf("admin on the tailnet got %d", code)
	}
}
