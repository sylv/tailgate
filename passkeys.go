package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/sylv/tailgate/store"
)

// passkeys are on the tailgate node's own name, so they work from the tailnet
// and from funnel, and stop working if that name changes
func (g *gate) passkeyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/passkeys/login", g.api(false, g.passkeyLoginBegin))
	mux.HandleFunc("POST /api/passkeys/login/{id}", g.api(false, g.passkeyLoginFinish))
	mux.HandleFunc("POST /api/passkeys", g.api(false, g.passkeyAddBegin))
	mux.HandleFunc("POST /api/passkeys/{id}", g.api(false, g.passkeyAddFinish))
	mux.HandleFunc("POST /api/passkeys/skip", g.api(false, func(_ http.ResponseWriter, _ *http.Request, id *identity) (any, error) {
		if id == nil {
			return nil, &apiError{http.StatusUnauthorized, "Sign in first"}
		}
		return nil, g.db.SkipPasskeyPrompt(id.user.ID)
	}))
}

func (g *gate) webauthn() (*webauthn.WebAuthn, error) {
	host := g.selfHost()
	return webauthn.New(&webauthn.Config{RPID: host, RPDisplayName: "tailgate", RPOrigins: []string{"https://" + host}})
}

// passkeyUser is a user with their passkeys, as go-webauthn wants them
type passkeyUser struct {
	*store.User
	creds []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte                         { return []byte(u.ID) }
func (u *passkeyUser) WebAuthnName() string                       { return u.Name }
func (u *passkeyUser) WebAuthnDisplayName() string                { return u.Name }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (g *gate) passkeyUser(u *store.User) (*passkeyUser, []store.Passkey, error) {
	list, err := g.db.Passkeys(u.ID)
	if err != nil {
		return nil, nil, err
	}
	pu := &passkeyUser{User: u}
	for _, p := range list {
		var c webauthn.Credential
		if err := json.Unmarshal(p.Credential, &c); err != nil {
			return nil, nil, err
		}
		pu.creds = append(pu.creds, c)
	}
	return pu, list, nil
}

type ceremony struct {
	session *webauthn.SessionData
	userID  string
}

// passkeyAddBegin starts adding a passkey for the signed in user. Only a key or
// passkey session can, so a tailnet device can't make itself a way in from funnel
func (g *gate) passkeyAddBegin(_ http.ResponseWriter, _ *http.Request, id *identity) (any, error) {
	if id == nil || id.via != "session" {
		return nil, &apiError{http.StatusForbidden, "Sign in with your key to add a passkey"}
	}
	wa, err := g.webauthn()
	if err != nil {
		return nil, err
	}
	pu, _, err := g.passkeyUser(id.user)
	if err != nil {
		return nil, err
	}
	var exclude []protocol.CredentialDescriptor
	for _, c := range pu.creds {
		exclude = append(exclude, c.Descriptor())
	}
	options, session, err := wa.BeginRegistration(pu, webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired), webauthn.WithExclusions(exclude))
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": g.ceremonies.add(ceremony{session, id.user.ID}, 5*time.Minute), "options": options}, nil
}

func (g *gate) passkeyAddFinish(_ http.ResponseWriter, r *http.Request, id *identity) (any, error) {
	c, ok := g.ceremonies.take(r.PathValue("id"))
	if !ok || id == nil || id.via != "session" || c.userID != id.user.ID {
		return nil, badRequest("That took too long, try again")
	}
	wa, err := g.webauthn()
	if err != nil {
		return nil, err
	}
	pu, _, err := g.passkeyUser(id.user)
	if err != nil {
		return nil, err
	}
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<16)
	cred, err := wa.FinishRegistration(pu, *c.session, r)
	if err != nil {
		return nil, badRequest("The passkey couldn't be checked: " + protocolError(err))
	}
	b, err := json.Marshal(cred)
	if err != nil {
		return nil, err
	}
	aaguid, _ := uuid.FromBytes(cred.Authenticator.AAGUID)
	if err := g.db.AddPasskey(id.user.ID, cred.ID, b, aaguid.String(), r.UserAgent()); err != nil {
		return nil, err
	}
	slog.Info("added a passkey", "user", id.user.Name)
	g.events.bump()
	return nil, nil
}

// passkeyLoginBegin starts a sign in where the browser picks the passkey, and
// with it the user
func (g *gate) passkeyLoginBegin(http.ResponseWriter, *http.Request, *identity) (any, error) {
	wa, err := g.webauthn()
	if err != nil {
		return nil, err
	}
	options, session, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": g.ceremonies.add(ceremony{session: session}, 5*time.Minute), "options": options}, nil
}

func (g *gate) passkeyLoginFinish(w http.ResponseWriter, r *http.Request, id *identity) (any, error) {
	c, ok := g.ceremonies.take(r.PathValue("id"))
	if !ok {
		return nil, badRequest("That took too long, try again")
	}
	wa, err := g.webauthn()
	if err != nil {
		return nil, err
	}
	var passkeyID string
	var user *store.User
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<16)
	cred, err := wa.FinishDiscoverableLogin(func(rawID, userHandle []byte) (webauthn.User, error) {
		pid, userID, err := g.db.PasskeyUser(rawID)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(userHandle, []byte(userID)) {
			return nil, errors.New("the passkey belongs to someone else")
		}
		if user, err = g.db.User(userID); err != nil {
			return nil, err
		}
		passkeyID = pid
		pu, _, err := g.passkeyUser(user)
		return pu, err
	}, *c.session, r)
	if err != nil {
		if passkeyID == "" {
			return nil, &apiError{http.StatusUnauthorized, "That passkey isn't one tailgate knows. It may have been removed"}
		}
		return nil, &apiError{http.StatusUnauthorized, "The passkey couldn't be checked: " + protocolError(err)}
	}
	b, err := json.Marshal(cred)
	if err != nil {
		return nil, err
	}
	if err := g.db.UsePasskey(passkeyID, b, r.UserAgent()); err != nil {
		return nil, err
	}
	return nil, g.startSession(w, r, id, user, store.Cred{PasskeyID: passkeyID})
}

// protocolError is the most useful part of a go-webauthn error
func protocolError(err error) string {
	var pe *protocol.Error
	if errors.As(err, &pe) && pe.DevInfo != "" {
		return pe.DevInfo
	}
	return err.Error()
}
