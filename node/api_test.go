package node

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sylv/tailgate/store"
)

// fakeAPI is the tailscale api with the given scope and tailscale services
func fakeAPI(t *testing.T, scope string, services map[string]apiService) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/tailnet/-/vip-services/")
		switch {
		case r.URL.Path == "/oauth/token":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "t", "expires_in": 3600, "scope": scope, "tags": tag})
		case r.Header.Get("Authorization") != "Bearer t":
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/tailnet/-/keys":
			json.NewEncoder(w).Encode(map[string]string{"key": "tskey-auth-new"})
		case r.URL.Path == "/tailnet/-/vip-services":
			var list []apiService
			for _, s := range services {
				list = append(list, s)
			}
			json.NewEncoder(w).Encode(map[string]any{"vipServices": list})
		case r.Method == http.MethodGet && services[name].Name != "":
			json.NewEncoder(w).Encode(services[name])
		case r.Method == http.MethodPut:
			var s apiService
			json.NewDecoder(r.Body).Decode(&s)
			services[name] = s
		case r.Method == http.MethodDelete && services[name].Name != "":
			delete(services, name)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old })
}

const secret = "tskey-client-kTEST-abc"

func TestAPI(t *testing.T) {
	ctx := context.Background()
	for _, scope := range []string{"services", "auth_keys devices:core:read services"} {
		fakeAPI(t, scope, map[string]apiService{})
		if _, err := NewAPI(ctx, secret); err == nil {
			t.Fatalf("a client with %s was accepted", scope)
		}
	}

	services := map[string]apiService{
		"svc:old":   {Name: "svc:old", Tags: []string{tag}},
		"svc:notes": {Name: "svc:notes"},
	}
	fakeAPI(t, "services auth_keys", services)
	a, err := NewAPI(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.define(ctx, "svc:notes"); err == nil {
		t.Error("a tailscale service tailgate didn't make was taken over")
	}
	if err := a.define(ctx, "svc:photos"); err != nil {
		t.Fatal(err)
	}
	if s := services["svc:photos"]; s.Tags[0] != tag || s.Ports[0] != "tcp:443" {
		t.Errorf("made %+v", s)
	}
	if got := strings.Join(a.defined(), " "); got != "svc:old svc:photos" {
		t.Errorf("defined is %s", got)
	}
	for _, name := range []string{"svc:old", "svc:notes"} {
		if err := a.undefine(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := services["svc:old"]; ok {
		t.Error("svc:old wasn't deleted")
	}
	if _, ok := services["svc:notes"]; !ok {
		t.Error("a tailscale service tailgate didn't make was deleted")
	}
}

// a node's state dir is dropped once the tailgate node stops seeing its device, and kept while it's there
func TestPrune(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "tailgate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, err := NewManager(dir, nil, db, func() {}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"photos": "nKEPT", "old": "nGONE", "funnel": "nGONE2"} {
		os.MkdirAll(filepath.Join(dir, "nodes", name), 0o700)
		m.saveDevice(name, id)
	}
	inUse := map[string]bool{"funnel": true}
	// nothing is pruned until the tailgate node has seen its peers
	if left := m.Unused(inUse); len(left) != 2 {
		t.Fatalf("left %v", left)
	}
	// or while it can't see a running node, which means the grant is missing
	m.peers = map[string]bool{"nKEPT": true}
	m.nodes["running"] = &Node{box: box{name: "running", status: Status{State: Online}}}
	m.saveDevice("running", "nHIDDEN")
	if left := m.Unused(inUse); len(left) != 2 || m.Problem() == "" {
		t.Fatalf("left %v with problem %q", left, m.Problem())
	}
	m.peers["nHIDDEN"] = true
	if left := m.Unused(inUse); len(left) != 1 || left[0] != "photos" {
		t.Errorf("left %v", left)
	}
	if _, err := os.Stat(filepath.Join(dir, "nodes", "old")); err == nil {
		t.Error("a deleted node's state was kept")
	}
	if _, err := os.Stat(filepath.Join(dir, "nodes", "funnel")); err != nil {
		t.Error("an in use node's state was dropped")
	}
	if nodes, _ := db.Nodes(); nodes["old"] != "" || len(nodes) != 3 {
		t.Errorf("saved nodes are %v", nodes)
	}
}
