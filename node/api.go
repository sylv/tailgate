package node

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// tailscale's docs call them /services, vip-services is the path whose json their own
// kubernetes operator relies on
var apiBase = "https://api.tailscale.com/api/v2"

var errNotFound = errors.New("not found")

// API is the tailscale API through an oauth client. tailgate uses it to make
// auth keys for new nodes, to manage its tailscale services and to notice
// devices and tailscale services deleted in the admin console
type API struct {
	secret string

	mu     sync.Mutex
	token  string
	expiry time.Time
	// ours is the tailscale services tailgate made, by name like svc:photos
	ours map[string]bool
}

// scopes is exactly what tailgate needs, so a leaked secret can't do more
var scopes = []string{"auth_keys", "devices:core:read", "services"}

// NewAPI checks the oauth client and reads which tailscale services are tailgate's
func NewAPI(ctx context.Context, secret string) (*API, error) {
	a := &API{secret: secret, ours: map[string]bool{}}
	tok, err := a.fetchToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("the oauth client in TS_CLIENT_SECRET doesn't work: %w", err)
	}
	got, tags := strings.Fields(tok.Scope), strings.Fields(tok.Tags)
	slices.Sort(got)
	if !slices.Equal(got, scopes) || !slices.Equal(tags, []string{tag}) {
		return nil, fmt.Errorf("the oauth client has to have exactly auth keys and services with write access and devices core with read access, tagged %s. it has %q tagged %q", tag, tok.Scope, tok.Tags)
	}
	if a.ours, err = a.services(ctx); err != nil {
		return nil, err
	}
	return a, nil
}

// services lists tailgate's tailscale services
func (a *API) services(ctx context.Context) (map[string]bool, error) {
	var list struct {
		VIPServices []apiService `json:"vipServices"`
	}
	if err := a.do(ctx, http.MethodGet, "/tailnet/-/vip-services", nil, &list); err != nil {
		return nil, fmt.Errorf("listing tailscale services failed: %w", err)
	}
	ours := map[string]bool{}
	for _, s := range list.VIPServices {
		if slices.Contains(s.Tags, tag) {
			ours[s.Name] = true
		}
	}
	return ours, nil
}

func (a *API) forget(name string) {
	a.mu.Lock()
	delete(a.ours, name)
	a.mu.Unlock()
}

// devices lists the stable ids of the devices in the tailnet
func (a *API) devices(ctx context.Context) (map[string]bool, error) {
	var list struct {
		Devices []struct {
			NodeID string `json:"nodeId"`
		} `json:"devices"`
	}
	if err := a.do(ctx, http.MethodGet, "/tailnet/-/devices", nil, &list); err != nil {
		return nil, fmt.Errorf("listing devices failed: %w", err)
	}
	ids := map[string]bool{}
	for _, d := range list.Devices {
		ids[d.NodeID] = true
	}
	return ids, nil
}

// authKey makes a single use auth key for one new node. Under tailnet lock
// it isn't signed, so the node waits to be signed
func (a *API) authKey(ctx context.Context) (string, error) {
	body := map[string]any{
		"capabilities": map[string]any{"devices": map[string]any{"create": map[string]any{
			"reusable": false, "ephemeral": false, "preauthorized": true, "tags": []string{tag},
		}}},
		"expirySeconds": 600,
		"description":   "tailgate node",
	}
	var res struct{ Key string }
	if err := a.do(ctx, http.MethodPost, "/tailnet/-/keys", body, &res); err != nil {
		return "", fmt.Errorf("making an auth key failed: %w", err)
	}
	return res.Key, nil
}

// apiService is a tailscale service definition. tailgate's are tagged tag:tailgate
type apiService struct {
	Name    string   `json:"name"`
	Addrs   []string `json:"addrs,omitempty"`
	Comment string   `json:"comment,omitempty"`
	Ports   []string `json:"ports,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

// define makes the tailscale service called name unless tailgate already has it
func (a *API) define(ctx context.Context, name string) error {
	a.mu.Lock()
	ok := a.ours[name]
	a.mu.Unlock()
	if ok {
		return nil
	}
	var s apiService
	err := a.do(ctx, http.MethodGet, "/tailnet/-/vip-services/"+name, nil, &s)
	switch {
	case errors.Is(err, errNotFound):
		s = apiService{Name: name, Comment: "made by tailgate", Ports: []string{"tcp:443"}, Tags: []string{tag}}
		if err := a.do(ctx, http.MethodPut, "/tailnet/-/vip-services/"+name, s, nil); err != nil {
			return fmt.Errorf("creating the tailscale service %s failed: %w", name, err)
		}
		slog.Info("created tailscale service", "service", name)
	case err != nil:
		return fmt.Errorf("reading the tailscale service %s failed: %w", name, err)
	case !slices.Contains(s.Tags, tag):
		return fmt.Errorf("there's already a tailscale service called %s. tailgate only uses services tagged %s, so delete or rename it in the tailscale admin console", name, tag)
	}
	a.mu.Lock()
	a.ours[name] = true
	a.mu.Unlock()
	return nil
}

// undefine deletes the tailscale service called name if tailgate made it
func (a *API) undefine(ctx context.Context, name string) error {
	a.mu.Lock()
	ok := a.ours[name]
	a.mu.Unlock()
	if !ok {
		return nil
	}
	if err := a.do(ctx, http.MethodDelete, "/tailnet/-/vip-services/"+name, nil, nil); err != nil && !errors.Is(err, errNotFound) {
		return fmt.Errorf("deleting the tailscale service %s failed: %w", name, err)
	}
	slog.Info("deleted tailscale service", "service", name)
	a.mu.Lock()
	delete(a.ours, name)
	a.mu.Unlock()
	return nil
}

func (a *API) defined() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Sorted(maps.Keys(a.ours))
}

type token struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	// Scope and Tags are space separated, everything the client has
	Scope string `json:"scope"`
	Tags  string `json:"tags"`
}

// fetchToken gets a new access token. tailscale ignores client_id, the secret says which client it is
func (a *API) fetchToken(ctx context.Context) (token, error) {
	form := url.Values{"client_secret": {a.secret}, "grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var res token
	if err := send(req, &res); err != nil {
		return token{}, err
	}
	a.mu.Lock()
	a.token, a.expiry = res.AccessToken, time.Now().Add(time.Duration(res.ExpiresIn)*time.Second)
	a.mu.Unlock()
	return res, nil
}

func (a *API) do(ctx context.Context, method, path string, body, out any) error {
	a.mu.Lock()
	token, expired := a.token, time.Until(a.expiry) < time.Minute
	a.mu.Unlock()
	if expired {
		if _, err := a.fetchToken(ctx); err != nil {
			return err
		}
		a.mu.Lock()
		token = a.token
		a.mu.Unlock()
	}
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return send(req, out)
}

var client = &http.Client{Timeout: 30 * time.Second}

func send(req *http.Request, out any) error {
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if res.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
		var e struct{ Message string }
		json.Unmarshal(b, &e)
		return fmt.Errorf("tailscale api said %s: %s", res.Status, cmp.Or(e.Message, strings.TrimSpace(string(b))))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}
