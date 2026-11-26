package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/sylv/tailgate/store"
)

const (
	authorizePath = "/oidc/authorize"
	tokenLifetime = time.Hour
)

type oidcCode struct {
	client, redirectURI, userID, nonce, challenge string
}

type accessToken struct {
	client, userID string
}

func (g *gate) oidc(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/openid-configuration", g.discovery)
	mux.HandleFunc("GET "+authorizePath, g.authorize)
	mux.HandleFunc("POST /oidc/token", g.token)
	mux.HandleFunc("GET /oidc/jwks", g.jwks)
	mux.HandleFunc("GET /oidc/userinfo", g.userinfo)
	mux.HandleFunc("POST /oidc/userinfo", g.userinfo)
}

func (g *gate) issuer() string { return "https://" + g.hosts("") }

func (g *gate) discovery(w http.ResponseWriter, _ *http.Request) {
	iss := g.issuer()
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                iss,
		"authorization_endpoint":                iss + authorizePath,
		"token_endpoint":                        iss + "/oidc/token",
		"userinfo_endpoint":                     iss + "/oidc/userinfo",
		"jwks_uri":                              iss + "/oidc/jwks",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "profile", "email"},
		"claims_supported":                      []string{"sub", "name", "preferred_username", "email", "email_verified"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

// authorize sends a code back to the app, or shows the login page, which
// reloads this url once the visitor has signed in
func (g *gate) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirectURI := q.Get("redirect_uri")
	c, err := g.db.OIDCClient(q.Get("client_id"))
	if errors.Is(err, store.ErrNotFound) || err == nil && !slices.Contains(c.RedirectURIs, redirectURI) {
		// never redirect to a uri that isn't the client's own
		plain(w, r, http.StatusBadRequest, "This app's sign in link doesn't match an OIDC client in tailgate. Check its client ID and redirect URI")
		return
	}
	if err != nil {
		slog.Error("reading the oidc client failed", "request_id", requestOf(r.Context()).id, "error", err.Error())
		plain(w, r, http.StatusInternalServerError, "Signing in failed")
		return
	}
	back := func(v url.Values) {
		if s := q.Get("state"); s != "" {
			v.Set("state", s)
		}
		sep := "?"
		if strings.Contains(redirectURI, "?") {
			sep = "&"
		}
		redirect(w, redirectURI+sep+v.Encode())
	}
	fail := func(code, desc string) { back(url.Values{"error": {code}, "error_description": {desc}}) }
	switch {
	case q.Get("response_type") != "code":
		fail("unsupported_response_type", "Only the code flow is supported")
		return
	case !slices.Contains(strings.Fields(q.Get("scope")), "openid"):
		fail("invalid_scope", "The openid scope is required")
		return
	case q.Has("code_challenge") && q.Get("code_challenge_method") != "S256":
		fail("invalid_request", "Only S256 code challenges are supported")
		return
	}
	id, err := g.identify(w, r, "")
	if err != nil {
		slog.Error("reading credentials failed", "request_id", requestOf(r.Context()).id, "error", err.Error())
		plain(w, r, http.StatusInternalServerError, "Signing in failed")
		return
	}
	if id == nil || !canSignIn(id.user, c) {
		g.page(w, r)
		return
	}
	back(url.Values{"code": {g.oidcCodes.add(oidcCode{client: c.ID, redirectURI: redirectURI, userID: id.user.ID, nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}, time.Minute)}})
}

func (g *gate) token(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	fail := func(status int, code string) { writeJSON(w, status, map[string]string{"error": code}) }
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := r.ParseForm(); err != nil {
		fail(http.StatusBadRequest, "invalid_request")
		return
	}
	clientID, secret, ok := r.BasicAuth()
	if !ok {
		clientID, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	c, err := g.db.OIDCClientBySecret(clientID, secret)
	if errors.Is(err, store.ErrNotFound) {
		fail(http.StatusUnauthorized, "invalid_client")
		return
	}
	if err != nil {
		g.oidcError(w, r, err)
		return
	}
	if r.PostForm.Get("grant_type") != "authorization_code" {
		fail(http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	cd, ok := g.oidcCodes.take(r.PostForm.Get("code"))
	if !ok || cd.client != c.ID || cd.redirectURI != r.PostForm.Get("redirect_uri") || cd.challenge != "" && pkce(r.PostForm.Get("code_verifier")) != cd.challenge {
		fail(http.StatusBadRequest, "invalid_grant")
		return
	}
	u, err := g.db.User(cd.userID)
	if errors.Is(err, store.ErrNotFound) || err == nil && !canSignIn(u, c) {
		fail(http.StatusBadRequest, "invalid_grant")
		return
	}
	if err != nil {
		g.oidcError(w, r, err)
		return
	}
	now := time.Now()
	claims := userClaims(u)
	claims["iss"], claims["aud"], claims["iat"], claims["exp"] = g.issuer(), c.ID, now.Unix(), now.Add(tokenLifetime).Unix()
	if cd.nonce != "" {
		claims["nonce"] = cd.nonce
	}
	idToken, err := g.sign(claims)
	if err != nil {
		g.oidcError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": g.access.add(accessToken{client: c.ID, userID: u.ID}, tokenLifetime),
		"token_type":   "Bearer",
		"expires_in":   int(tokenLifetime.Seconds()),
		"id_token":     idToken,
	})
}

func (g *gate) userinfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	t, ok := g.access.get(bearer(r.Header))
	var c *store.OIDCClient
	var u *store.User
	var err error
	if ok {
		if c, err = g.db.OIDCClient(t.client); err == nil {
			u, err = g.db.User(t.userID)
		}
	}
	if !ok || errors.Is(err, store.ErrNotFound) || err == nil && !canSignIn(u, c) {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	if err != nil {
		g.oidcError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, userClaims(u))
}

// canSignIn is whether u can sign in to the client's app. Most apps match or
// make accounts by email and fail in their own confusing way without one, so
// an email is required
func canSignIn(u *store.User, c *store.OIDCClient) bool {
	return u.HasAccess(c.Service) && u.Email != ""
}

func userClaims(u *store.User) map[string]any {
	// an admin typed the email in, and some apps won't match an account by an unverified one
	return map[string]any{"sub": u.ID, "name": u.Name, "preferred_username": u.Name, "email": u.Email, "email_verified": true}
}

func (g *gate) oidcError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("oidc request failed", "request_id", requestOf(r.Context()).id, "path", r.URL.Path, "error", err.Error())
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
}

func (g *gate) jwks(w http.ResponseWriter, r *http.Request) {
	key, err := g.signingKey()
	if err != nil {
		g.oidcError(w, r, err)
		return
	}
	b64 := base64.RawURLEncoding.EncodeToString
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": keyID(key),
		"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
	}}})
}

func (g *gate) signingKey() (*rsa.PrivateKey, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.signer == nil {
		key, err := g.db.SigningKey()
		if err != nil {
			return nil, err
		}
		g.signer = key
	}
	return g.signer, nil
}

func keyID(key *rsa.PrivateKey) string {
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	sum := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

// sign makes an RS256 JWT
func (g *gate) sign(claims map[string]any) (string, error) {
	key, err := g.signingKey()
	if err != nil {
		return "", err
	}
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": keyID(key)})
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	b64 := base64.RawURLEncoding.EncodeToString
	signed := b64(header) + "." + b64(body)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signed + "." + b64(sig), nil
}

func pkce(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
