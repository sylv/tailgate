package main

import (
	"crypto/rand"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sylv/tailgate/store"
)

const (
	cookieName = "__Host-tailgate_session"
	// stateCookie ties a callback code to the browser that was sent to sign in, see authorized
	stateCookie = "__Host-tailgate_state"
	keyHeader   = "X-Tailgate-Key"
	keyPrefix   = "tg_"
)

type identity struct {
	user *store.User
	// via is session, key or tailnet
	via     string
	session string
	cred    store.Cred
	// login is the linked tailnet login when via is tailnet
	login string
}

// identify reads a request's credentials in order and returns nil when there
// are none. host is the app, or "" for the tailgate node, which doesn't
// take the header keys apps use
func (g *gate) identify(w http.ResponseWriter, r *http.Request, host string) (*identity, error) {
	id, err := g.credentials(w, r, host)
	if id != nil {
		req := requestOf(r.Context())
		req.user, req.via = id.user.Name, id.via
	}
	return id, err
}

func (g *gate) credentials(w http.ResponseWriter, r *http.Request, host string) (*identity, error) {
	if c, err := r.Cookie(cookieName); err == nil {
		s, err := g.db.Session(c.Value, host)
		if err == nil {
			if s.Refreshed {
				setCookie(w, c.Value, s.Expires)
			}
			return &identity{user: s.User, via: "session", session: s.ID, cred: s.Cred}, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	if host != "" {
		for _, key := range []string{bearer(r.Header), r.Header.Get(keyHeader)} {
			if !strings.HasPrefix(key, keyPrefix) {
				continue
			}
			u, keyID, err := g.db.KeyUser(key)
			if err == nil {
				return &identity{user: u, via: "key", cred: store.Cred{KeyID: keyID}}, nil
			}
			if !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
		}
	}
	p, err := g.peer(r.Context())
	if err != nil {
		return nil, err
	}
	// p is nil for funnel
	if p == nil || !p.Linkable() {
		return nil, nil
	}
	u, err := g.db.LoginUser(p.LoginID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &identity{user: u, via: "tailnet", login: p.Login}, nil
}

func bearer(h http.Header) string {
	scheme, token, _ := strings.Cut(h.Get("Authorization"), " ")
	if !strings.EqualFold(scheme, "bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func setCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", MaxAge: int(time.Until(expires).Seconds()), Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

// stripCredentials removes every tailgate credential so none reach the app.
// a credential without the tg_ prefix belongs to the app and stays
func stripCredentials(h http.Header) {
	var cookies []string
	for _, line := range h.Values("Cookie") {
		for c := range strings.SplitSeq(line, ";") {
			if c = strings.TrimSpace(c); c != "" && !strings.HasPrefix(c, cookieName+"=") {
				cookies = append(cookies, c)
			}
		}
	}
	h.Del("Cookie")
	if len(cookies) > 0 {
		h.Set("Cookie", strings.Join(cookies, "; "))
	}
	h.Del(keyHeader)
	if strings.HasPrefix(bearer(h), keyPrefix) {
		h.Del("Authorization")
	}
}

// navigation is whether the browser is loading a page, which gets the login
// redirect instead of a 401
func navigation(r *http.Request) bool {
	return r.Header.Get("Sec-Fetch-Mode") == "navigate" || r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html")
}

// code is a single use code that swaps a tailgate node session for a session on an app.
// state is the app's state cookie in the browser that was sent to sign in
type code struct {
	app, path, state string
	userID, parent   string
	cred             store.Cred
}

// tokens holds short lived codes and tokens in memory, so a restart ends them
type tokens[T any] struct {
	mu sync.Mutex
	m  map[string]expiring[T]
}

type expiring[T any] struct {
	v       T
	expires time.Time
}

func (t *tokens[T]) add(v T, ttl time.Duration) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for id, old := range t.m {
		if now.After(old.expires) {
			delete(t.m, id)
		}
	}
	if t.m == nil {
		t.m = map[string]expiring[T]{}
	}
	id := rand.Text()
	t.m[id] = expiring[T]{v, now.Add(ttl)}
	return id
}

func (t *tokens[T]) get(id string) (T, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.m[id]
	return e.v, ok && time.Now().Before(e.expires)
}

// take is get for single use codes, under one lock so two requests can't both use a code
func (t *tokens[T]) take(id string) (T, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.m[id]
	delete(t.m, id)
	return e.v, ok && time.Now().Before(e.expires)
}
