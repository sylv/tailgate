package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"github.com/klauspost/compress/gzhttp"

	"github.com/sylv/tailgate/app"
	"github.com/sylv/tailgate/node"
	"github.com/sylv/tailgate/rules"
	"github.com/sylv/tailgate/store"
)

const callbackPath = "/.tailgate/callback"

type appKey struct{}

// compressed compresses responses for clients that take it. It goes outside requests, so
// websockets can reach the connection. Event streams stay plain, gzhttp holds a response
// back until something is written, and a stream's events are small anyway
var compressed = func() func(http.Handler) http.HandlerFunc {
	wrap, err := gzhttp.NewWrapper(gzhttp.ExceptContentTypes([]string{"text/event-stream"}))
	if err != nil {
		panic(err)
	}
	return wrap
}()

func newProxy() (*http.Transport, *httputil.ReverseProxy) {
	transport := &http.Transport{
		DialContext:     (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:    1024,
		IdleConnTimeout: 90 * time.Second,
		// go keeps 2 idle connections per host by default, too few for apps that load many assets at once
		MaxIdleConnsPerHost: 64,
	}
	return transport, &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			s := r.In.Context().Value(appKey{}).(app.App)
			r.SetURL(&url.URL{Scheme: "http", Host: net.JoinHostPort(s.Addr, strconv.Itoa(s.Port))})
			r.Out.Host = r.In.Host
			requestOf(r.In.Context()).rules.ApplyRequest(r.Out.Header)
			stripCredentials(r.Out.Header)
			r.SetXForwarded()
			ip, _, _ := net.SplitHostPort(r.In.RemoteAddr)
			r.Out.Header.Set("X-Real-IP", ip)
			r.Out.Header.Set(requestIDHeader, requestOf(r.In.Context()).id)
		},
		ModifyResponse: func(res *http.Response) error {
			// tailgate already set its own on the response
			res.Header.Del(requestIDHeader)
			if res.Request.Context().Value(appKey{}).(app.App).Noindex {
				res.Header.Set("X-Robots-Tag", noindex)
			}
			requestOf(res.Request.Context()).rules.ApplyResponse(res.Header)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Warn("app request failed", "request_id", requestOf(r.Context()).id, "host", r.Host, "error", err.Error())
			plain(w, r, http.StatusBadGateway, "app is offline")
		},
	}
}

// appHandler serves an app's host. Pre-auth rules run first, then
// the callback and the login and access checks, then post-auth rules
func (g *gate) appHandler(name string) http.Handler {
	return compressed(requests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := g.route(name)
		if !ok {
			plain(w, r, http.StatusBadGateway, "app unavailable")
			return
		}
		set, err := g.ruleSet()
		if err != nil {
			plain(w, r, http.StatusServiceUnavailable, "app unavailable")
			return
		}
		var id *identity
		if !s.DangerouslyDisableAuth {
			if id, err = g.identify(w, r, name); err != nil {
				slog.Error("reading credentials failed", "request_id", requestOf(r.Context()).id, "error", err.Error())
				plain(w, r, http.StatusInternalServerError, "sign in failed")
				return
			}
		}
		req, err := g.ruleRequest(r, s)
		if err != nil {
			slog.Error("reading the request for rules failed", "request_id", requestOf(r.Context()).id, "error", err.Error())
			plain(w, r, http.StatusInternalServerError, "internal server error")
			return
		}
		if r.URL.Path == tracePath {
			trace(w, r, req, id)
			return
		}
		user := rules.User{}
		if id != nil {
			user = rules.User{SignedIn: true, ID: id.user.ID, Name: id.user.Name, Email: id.user.Email, Admin: id.user.Admin, HasAccess: id.user.HasAccess(name), Via: id.via}
		}
		res := requestOf(r.Context()).rules
		if ended(w, r, set.Run(rules.Pre, req, user, res), res) {
			return
		}
		if !s.DangerouslyDisableAuth && !g.authorized(w, r, name, id) {
			return
		}
		if ended(w, r, set.Run(rules.Post, req, user, res), res) {
			return
		}
		g.proxy(w, r, s)
	})))
}

// authorized is the auth section. It answers the callback and stops requests
// that aren't signed in or don't have access
func (g *gate) authorized(w http.ResponseWriter, r *http.Request, name string, id *identity) bool {
	if r.URL.Path == callbackPath {
		g.callback(w, r, name)
		return false
	}
	if id == nil {
		if !navigation(r) {
			plain(w, r, http.StatusUnauthorized, "sign in required")
			return false
		}
		// the callback only takes a code made for this browser, so nobody can send someone a
		// link that signs them in as somebody else
		state := rand.Text()
		http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: state, Path: "/", MaxAge: 600, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		redirect(w, "https://"+g.selfHost()+"/login?"+url.Values{"to": {name + ":" + r.URL.RequestURI()}, "state": {state}}.Encode())
		return false
	}
	if !id.user.HasAccess(name) {
		plain(w, r, http.StatusForbidden, "you do not have access to this app")
		return false
	}
	return true
}

// ended answers a request a rule blocked or redirected, or that failed while
// running the rules, which blocks it too
func ended(w http.ResponseWriter, r *http.Request, err error, res *rules.Result) bool {
	req := requestOf(r.Context())
	if err != nil {
		req.ruleErr = err.Error()
		plain(w, r, http.StatusForbidden, "your request was blocked")
		return true
	}
	if res.End == nil {
		return false
	}
	req.rule = res.End.Rule
	if res.End.URL == "" {
		plain(w, r, http.StatusForbidden, cmp.Or(res.End.Message, "your request was blocked"))
		return true
	}
	w.Header().Set("Location", res.End.URL)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(res.End.Status)
	return true
}

// ruleRequest is the values rules see for a request
func (g *gate) ruleRequest(r *http.Request, s app.App) (rules.Request, error) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	// rules see the headers the app will, so they can't copy a credential somewhere it isn't stripped from
	seen := &http.Request{Header: r.Header.Clone()}
	stripCredentials(seen.Header)
	req := rules.Request{
		Ingress:   node.Ingress(r.Context()),
		App:       s,
		Host:      r.Host,
		Method:    r.Method,
		Path:      r.URL.Path,
		Query:     r.URL.RawQuery,
		IP:        ip,
		UserAgent: r.UserAgent(),
		Header:    seen.Header.Get,
		Cookie: func(name string) string {
			if c, err := seen.Cookie(name); err == nil {
				return c.Value
			}
			return ""
		},
		Param: r.URL.Query().Get,
	}
	if req.Ingress == "funnel" && g.geo != nil {
		req.Country = g.geo.Country(ip)
	}
	p, err := g.peer(r.Context())
	if err != nil {
		return req, err
	}
	if p != nil {
		req.Tailnet = rules.Tailnet{Node: p.Node, Tags: p.Tags}
		if p.Linkable() {
			req.Tailnet.Login = p.Login
		}
	}
	return req, nil
}

func (g *gate) proxy(w http.ResponseWriter, r *http.Request, s app.App) {
	if s.Addr == "" {
		plain(w, r, http.StatusBadGateway, "app is offline")
		return
	}
	g.rp.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), appKey{}, s)))
}

func (g *gate) callback(w http.ResponseWriter, r *http.Request, name string) {
	c, ok := g.codes.take(r.URL.Query().Get("code"))
	if !ok || c.app != name {
		plain(w, r, http.StatusNotFound, "404 page not found")
		return
	}
	state, err := r.Cookie(stateCookie)
	switch {
	case err != nil:
		plain(w, r, http.StatusBadRequest, "sign in failed, your browser didn't keep tailgate's cookie")
		return
	case state.Value != c.state:
		// another tab started signing in since, or the link isn't this browser's. going
		// back to the page signs in again with this browser's own state
		redirect(w, c.path)
		return
	}
	token, err := g.db.CreateSession(c.userID, c.cred, name, c.parent)
	if err != nil {
		slog.Error("creating a session failed", "request_id", requestOf(r.Context()).id, "error", err.Error())
		plain(w, r, http.StatusInternalServerError, "sign in failed")
		return
	}
	clearCookie(w, stateCookie)
	setCookie(w, token, time.Now().Add(store.SessionLifetime))
	redirect(w, c.path)
}
