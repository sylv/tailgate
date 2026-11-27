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
)

// fakeAPI is the tailscale api with the given scope, tailscale services and device ids
func fakeAPI(t *testing.T, scope string, services map[string]apiService, devices ...string) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/tailnet/-/vip-services/")
		switch {
		case r.URL.Path == "/oauth/token":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "t", "expires_in": 3600, "scope": scope, "tags": tag})
		case r.Header.Get("Authorization") != "Bearer t":
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/tailnet/-/devices":
			var list []map[string]string
			for _, id := range devices {
				list = append(list, map[string]string{"nodeId": id})
			}
			json.NewEncoder(w).Encode(map[string]any{"devices": list})
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
	for _, scope := range []string{"devices:core:read services", "auth_keys devices:core services"} {
		fakeAPI(t, scope, map[string]apiService{})
		if _, err := NewAPI(ctx, secret); err == nil {
			t.Fatalf("a client with %s was accepted", scope)
		}
	}

	services := map[string]apiService{
		"svc:old":   {Name: "svc:old", Tags: []string{tag}},
		"svc:notes": {Name: "svc:notes"},
	}
	fakeAPI(t, "auth_keys services devices:core:read", services)
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

// a node's state dir is dropped once its device is gone from the tailnet, and kept while it's there
func TestPrune(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	state := func(name, id string) {
		profiles, _ := json.Marshal(map[string]any{"a5a4": map[string]string{"ID": "a5a4", "Key": "profile-a5a4", "NodeID": id}})
		b, _ := json.Marshal(map[string][]byte{"_current-profile": []byte("profile-a5a4"), "_profiles": profiles})
		os.MkdirAll(filepath.Join(dir, "nodes", name), 0o700)
		os.WriteFile(filepath.Join(dir, "nodes", name, "tailscaled.state"), b, 0o600)
	}
	state("photos", "nKEPT")
	state("old", "nGONE")
	state("funnel", "nGONE2")
	fakeAPI(t, "auth_keys services devices:core:read", map[string]apiService{"svc:notes": {Name: "svc:notes", Tags: []string{tag}}}, "nKEPT")
	a, err := NewAPI(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir, a)
	// notes was deleted in the admin console after tailgate started
	fakeAPI(t, "auth_keys services devices:core:read", map[string]apiService{}, "nKEPT")
	pruned, err := m.Prune(ctx, map[string]bool{"funnel": true})
	if err != nil || !pruned {
		t.Fatal(pruned, err)
	}
	left, _ := m.Unused(map[string]bool{"funnel": true})
	if len(left) != 1 || left[0] != "photos" {
		t.Errorf("left %v", left)
	}
	if _, err := os.Stat(filepath.Join(dir, "nodes", "funnel")); err != nil {
		t.Error("an in use node's state was dropped")
	}
}
