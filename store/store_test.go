package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sylv/tailgate/rules"
)

func open(t *testing.T) *Store {
	s, err := Open(filepath.Join(t.TempDir(), "tailgate.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSetup(t *testing.T) {
	s := open(t)
	first, err := s.StartSetup()
	if err != nil || !strings.HasPrefix(first, "tgs_") {
		t.Fatalf("got %q, %v", first, err)
	}
	// a restart while setup is pending makes a new setup key
	setupKey, _ := s.StartSetup()
	if _, err := s.FinishSetup(first, "sylv"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old setup key got %v", err)
	}
	if pending, _ := s.SetupPending(); !pending {
		t.Fatal("setup isn't pending")
	}
	if _, _, err := s.KeyUser(setupKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("signing in with the setup key got %v", err)
	}
	key, err := s.FinishSetup(setupKey, "sylv")
	if err != nil {
		t.Fatal(err)
	}
	u, _, err := s.KeyUser(key)
	if err != nil || u.Name != "sylv" || !u.Admin || !u.HasAccess("anything") {
		t.Fatalf("got %+v, %v", u, err)
	}
	if _, err := s.FinishSetup(setupKey, "someone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second setup got %v", err)
	}
	if pending, _ := s.SetupPending(); pending {
		t.Fatal("setup is still pending")
	}
	if again, err := s.StartSetup(); again != "" || err != nil {
		t.Fatalf("setup started again with %q, %v", again, err)
	}
}

func TestUsers(t *testing.T) {
	s := open(t)
	id, err := s.SaveUser(User{Name: "ezra", Note: "iPad", Services: []string{"photos", "lyra"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveUser(User{Name: "ezra"}); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate name got %v", err)
	}
	if _, err := s.SaveUser(User{ID: id, Name: "ezra", Services: []string{"photos"}}); err != nil {
		t.Fatal(err)
	}
	u, err := s.User(id)
	if err != nil || !u.HasAccess("photos") || u.HasAccess("lyra") || u.Note != "" {
		t.Fatalf("got %+v, %v", u, err)
	}
	// only SetAdmin makes an admin
	if _, err := s.SaveUser(User{ID: id, Name: "ezra", Admin: true, Services: []string{"photos"}}); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.User(id); u.Admin {
		t.Fatal("SaveUser made an admin")
	}
	if err := s.SetAdmin(id, true); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.User(id); !u.Admin || !u.AllServices || !u.HasAccess("lyra") {
		t.Fatalf("admin got %+v", u)
	}
	// an admin's saved choice is kept for when they're demoted
	if _, err := s.SaveUser(User{ID: id, Name: "ezra", AllServices: true, Services: []string{"photos"}}); err != nil {
		t.Fatal(err)
	}
	s.SetAdmin(id, false)
	if u, _ := s.User(id); u.AllServices || u.HasAccess("lyra") {
		t.Fatalf("demoted admin got %+v", u)
	}
	if _, err := s.SaveUser(User{ID: "nope", Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id got %v", err)
	}

	if err := s.LinkLogin(id, Login{ID: 42, Name: "ezra@"}); err != nil {
		t.Fatal(err)
	}
	other, _ := s.SaveUser(User{Name: "juno"})
	if err := s.LinkLogin(other, Login{ID: 42, Name: "ezra@"}); !errors.Is(err, ErrExists) {
		t.Fatalf("login on two users got %v", err)
	}
	if u, err := s.LoginUser(42); err != nil || u.ID != id {
		t.Fatalf("got %+v, %v", u, err)
	}
	if err := s.UnlinkLogin(42); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoginUser(42); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unlinked login got %v", err)
	}
}

func TestSessions(t *testing.T) {
	s := open(t)
	id, _ := s.SaveUser(User{Name: "ezra", Services: []string{"photos"}})
	key, err := s.AddKey(id, "ipad")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.KeyUser("tg_wrong"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong key got %v", err)
	}
	u, keyID, err := s.KeyUser(key)
	if err != nil || u.ID != id {
		t.Fatalf("got %+v, %v", u, err)
	}

	parent, err := s.CreateSession(id, keyID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ps, err := s.Session(parent, "")
	if err != nil || ps.User.Name != "ezra" {
		t.Fatalf("got %+v, %v", ps, err)
	}
	if _, err := s.Session(parent, "photos"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session on another host got %v", err)
	}
	child, err := s.CreateSession(id, keyID, "photos", ps.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(child, "photos"); err != nil {
		t.Fatal(err)
	}

	// signing out on the tailgate node ends the service sessions too
	if err := s.DeleteSession(ps.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(child, "photos"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("child after sign out got %v", err)
	}

	// revoking a key ends its sessions, and resetting revokes every key
	parent, _ = s.CreateSession(id, keyID, "", "")
	if _, err := s.ResetKeys(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(parent, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session after reset got %v", err)
	}
	if _, _, err := s.KeyUser(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old key after reset got %v", err)
	}

	// deleting the user ends everything
	key, _ = s.AddKey(id, "")
	_, keyID, _ = s.KeyUser(key)
	parent, _ = s.CreateSession(id, keyID, "", "")
	if err := s.DeleteUser(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(parent, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session after delete got %v", err)
	}
}

func TestRules(t *testing.T) {
	s := open(t)
	block := []rules.Effect{{Type: rules.Block, Value: `"no"`}}
	a := rules.Rule{ID: "a", Section: rules.Pre, Name: "a", Condition: "true", Effects: block, Mode: "on"}
	b := rules.Rule{ID: "b", Section: rules.Post, Name: "b", Condition: "false", Effects: block, Mode: "log"}
	if err := s.SaveRules([]rules.Rule{a, b}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRules([]rules.Rule{b, a}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Rules()
	if err != nil || len(got) != 2 || got[0].ID != "b" || got[1].Effects[0].Value != `"no"` {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tailgate.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.StartSetup()
	s.db.Exec(`PRAGMA user_version = 99`)
	s.Close()
	// a database from a newer tailgate isn't touched
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("got %v", err)
	}
	s, _ = Open(path + "2")
	s.Close()
	// opening again runs nothing twice
	s, err = Open(path + "2")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}
