package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sylv/tailgate/store"
)

func TestOIDC(t *testing.T) {
	tg := newTestGate(t)
	clientID, secret, _ := tg.db.SaveOIDCClient(store.OIDCClient{Name: "photos", App: "photos", RedirectURIs: []string{"https://photos.ts.net/auth/callback", "app.photos:///cb"}})
	userID, _ := tg.db.UserIDByName("ezra")
	_, keyID, _ := tg.db.KeyUser(tg.key)
	session, _ := tg.db.CreateSession(userID, keyID, "", "")
	signedIn := http.Header{"Cookie": {cookieName + "=" + session}}
	verifier := "a-long-enough-pkce-verifier-for-the-test"
	authorize := func(redirect, client string, h http.Header) *httptest.ResponseRecorder {
		q := url.Values{"response_type": {"code"}, "client_id": {client}, "redirect_uri": {redirect}, "scope": {"openid email"}, "state": {"st"}, "nonce": {"n1"},
			"code_challenge": {pkce(verifier)}, "code_challenge_method": {"S256"}}
		return do(tg.funnel, "GET", "https://tailgate.ts.net"+authorizePath+"?"+q.Encode(), h)
	}

	// a redirect uri that isn't the client's never gets a redirect
	for _, c := range [][2]string{{"https://evil.example/cb", clientID}, {"https://photos.ts.net/auth/callback", "nope"}} {
		if rec := authorize(c[0], c[1], signedIn); rec.Code != http.StatusBadRequest || rec.Header().Get("Location") != "" {
			t.Errorf("%v got %d %q", c, rec.Code, rec.Header().Get("Location"))
		}
	}
	if rec := authorize("app.photos:///cb", clientID, nil); rec.Header().Get("Location") != "" {
		t.Fatalf("signed out got a redirect to %q", rec.Header().Get("Location"))
	}

	rec := authorize("app.photos:///cb", clientID, signedIn)
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusFound || !strings.HasPrefix(loc, "app.photos:///cb?") {
		t.Fatalf("got %d %q", rec.Code, loc)
	}
	back, _ := url.Parse(loc)
	if back.Query().Get("state") != "st" {
		t.Errorf("lost the state in %q", loc)
	}
	token := func(code, verifier string) *httptest.ResponseRecorder {
		form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"app.photos:///cb"}, "code_verifier": {verifier}}
		req := httptest.NewRequest("POST", "https://tailgate.ts.net/oidc/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth(clientID, secret)
		rec := httptest.NewRecorder()
		tg.web.ServeHTTP(rec, req)
		return rec
	}
	code := back.Query().Get("code")
	if rec := token(code, "wrong"); rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong verifier got %d", rec.Code)
	}
	// the wrong verifier used the code up
	rec = authorize("app.photos:///cb", clientID, signedIn)
	back, _ = url.Parse(rec.Header().Get("Location"))
	code = back.Query().Get("code")
	rec = token(code, verifier)
	var res struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &res); rec.Code != http.StatusOK || res.IDToken == "" {
		t.Fatalf("token got %d %s", rec.Code, rec.Body)
	}
	if rec := token(code, verifier); rec.Code != http.StatusBadRequest {
		t.Errorf("reused code got %d", rec.Code)
	}

	// the id token verifies against the jwks
	var jwks struct{ Keys []struct{ N, E string } }
	json.Unmarshal(do(tg.web, "GET", "https://tailgate.ts.net/oidc/jwks", nil).Body.Bytes(), &jwks)
	n, _ := base64.RawURLEncoding.DecodeString(jwks.Keys[0].N)
	e, _ := base64.RawURLEncoding.DecodeString(jwks.Keys[0].E)
	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	parts := strings.Split(res.IDToken, ".")
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatal(err)
	}
	body, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	json.Unmarshal(body, &claims)
	if claims["sub"] != userID || claims["aud"] != clientID || claims["iss"] != "https://tailgate.ts.net" || claims["nonce"] != "n1" || claims["name"] != "ezra" || claims["preferred_username"] != "ezra" {
		t.Errorf("got claims %v", claims)
	}

	info := do(tg.web, "GET", "https://tailgate.ts.net/oidc/userinfo", http.Header{"Authorization": {"Bearer " + res.AccessToken}})
	if info.Code != http.StatusOK || !strings.Contains(info.Body.String(), `"sub":"`+userID+`"`) {
		t.Errorf("userinfo got %d %s", info.Code, info.Body)
	}
	// apps need an email, so without one there's no code, and the login page says why
	tg.db.SaveUser(store.User{ID: userID, Name: "ezra", Apps: []string{"photos"}})
	if rec := authorize("app.photos:///cb", clientID, signedIn); rec.Header().Get("Location") != "" {
		t.Errorf("no email got a redirect to %q", rec.Header().Get("Location"))
	}
	me := do(tg.web, "GET", "https://tailgate.ts.net/api/me?client="+clientID, signedIn)
	if !strings.Contains(me.Body.String(), "has no email") {
		t.Errorf("me without an email got %s", me.Body)
	}
	if info := do(tg.web, "GET", "https://tailgate.ts.net/oidc/userinfo", http.Header{"Authorization": {"Bearer " + res.AccessToken}}); info.Code != http.StatusUnauthorized {
		t.Errorf("userinfo without an email got %d", info.Code)
	}

	// losing access to the app ends the token
	tg.db.SaveUser(store.User{ID: userID, Name: "ezra", Email: "ezra@example.com"})
	if info := do(tg.web, "GET", "https://tailgate.ts.net/oidc/userinfo", http.Header{"Authorization": {"Bearer " + res.AccessToken}}); info.Code != http.StatusUnauthorized {
		t.Errorf("userinfo without access got %d", info.Code)
	}
	if rec := authorize("app.photos:///cb", clientID, signedIn); rec.Header().Get("Location") != "" {
		t.Errorf("no access got a redirect to %q", rec.Header().Get("Location"))
	}
}

func TestOIDCRedirectURIs(t *testing.T) {
	tg := newTestGate(t)
	id, _ := tg.db.UserIDByName("ezra")
	tg.db.SetAdmin(id, true)
	_, keyID, _ := tg.db.KeyUser(tg.key)
	session, _ := tg.db.CreateSession(id, keyID, "", "")
	for uri, ok := range map[string]bool{
		"https://photos.ts.net/cb":     true,
		"app.immich:///oauth-callback": true,
		"http://photos.ts.net/cb":      false,
		"javascript:alert(1)":          false,
		"/cb":                          false,
		"https://photos.ts.net/cb#x":   false,
	} {
		req := httptest.NewRequest("POST", "https://tailgate.ts.net/api/admin/oidc-clients", strings.NewReader(`{"name":"p","app":"photos","redirectURIs":["`+uri+`"]}`))
		req.Header = http.Header{"Origin": {"https://tailgate.ts.net"}, "Content-Type": {"application/json"}, "Cookie": {cookieName + "=" + session}}
		rec := httptest.NewRecorder()
		tg.web.ServeHTTP(rec, req)
		if (rec.Code == http.StatusOK) != ok {
			t.Errorf("%s got %d %s", uri, rec.Code, rec.Body)
		}
	}
}
