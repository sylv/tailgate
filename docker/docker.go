package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

type Container struct {
	ID     string
	Name   string
	Labels map[string]string
	// Networks maps each network the container is on to its IP there, "" while
	// it's stopped. With network_mode container:x they're x's networks
	Networks map[string]string
	// NetworkLabels are x's labels with network_mode container:x, since x can say which network to use
	NetworkLabels map[string]string
	// Host is set for network_mode host, to the default bridge's gateway, which is an address on the host
	Host string
}

type Client struct {
	http *http.Client
	base string
}

// New connects to host, which is DOCKER_HOST's format: unix:///path or tcp://host:port
func New(host string) (*Client, error) {
	if path, ok := strings.CutPrefix(host, "unix://"); ok {
		return &Client{base: "http://docker", http: &http.Client{Transport: UnixTransport(path)}}, nil
	}
	if addr, ok := strings.CutPrefix(host, "tcp://"); ok {
		return &Client{base: "http://" + addr, http: &http.Client{}}, nil
	}
	return nil, fmt.Errorf("DOCKER_HOST %q has to start with unix:// or tcp://", host)
}

// UnixTransport sends every request to the socket at path
func UnixTransport(path string) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		},
	}
}

func (c *Client) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("docker %s: %s", path, res.Status)
	}
	return json.NewDecoder(res.Body).Decode(v)
}

type apiContainer struct {
	ID         string `json:"Id"`
	Names      []string
	Labels     map[string]string
	HostConfig struct {
		NetworkMode string
	}
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string
		}
	}
}

// Containers lists every container, stopped ones too, with the address to reach it at
func (c *Client) Containers(ctx context.Context) ([]Container, error) {
	var list []apiContainer
	if err := c.get(ctx, "/containers/json?all=1", &list); err != nil {
		return nil, err
	}
	gateway := ""
	if slices.ContainsFunc(list, func(c apiContainer) bool { return c.HostConfig.NetworkMode == "host" }) {
		var bridge struct {
			IPAM struct {
				Config []struct{ Gateway string }
			}
		}
		if err := c.get(ctx, "/networks/bridge", &bridge); err != nil {
			return nil, err
		}
		for _, cfg := range bridge.IPAM.Config {
			if ip := net.ParseIP(cfg.Gateway); ip != nil && ip.To4() != nil {
				gateway = cfg.Gateway
				break
			}
		}
		if gateway == "" {
			return nil, errors.New("the default bridge network has no IPv4 gateway, which is how containers with network_mode host are reached")
		}
	}
	return resolve(list, gateway), nil
}

func resolve(list []apiContainer, gateway string) []Container {
	byRef := map[string]apiContainer{}
	for _, c := range list {
		byRef[c.ID] = c
		for _, n := range c.Names {
			byRef[strings.TrimPrefix(n, "/")] = c
		}
	}
	out := make([]Container, 0, len(list))
	for _, c := range list {
		name := c.ID
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		out = append(out, network(Container{ID: c.ID, Name: name, Labels: c.Labels}, c, byRef, gateway))
	}
	return out
}

func network(out Container, c apiContainer, byRef map[string]apiContainer, gateway string) Container {
	mode := c.HostConfig.NetworkMode
	if mode == "host" {
		out.Host = gateway
		return out
	}
	// network_mode: container:<id> has no ip of its own, it shares the other container's
	if ref, ok := strings.CutPrefix(mode, "container:"); ok {
		other, ok := byRef[ref]
		if !ok || strings.HasPrefix(other.HostConfig.NetworkMode, "container:") {
			return out
		}
		out = network(out, other, byRef, gateway)
		out.NetworkLabels = other.Labels
		return out
	}
	out.Networks = map[string]string{}
	for name, n := range c.NetworkSettings.Networks {
		out.Networks[name] = n.IPAddress
	}
	return out
}

// Watch calls fn for every container and network event until ctx ends or the stream breaks
func (c *Client) Watch(ctx context.Context, fn func()) error {
	filters := url.QueryEscape(`{"type":["container","network"]}`)
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/events?filters="+filters, nil)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("docker events: %s", res.Status)
	}
	dec := json.NewDecoder(res.Body)
	for {
		var ev json.RawMessage
		if err := dec.Decode(&ev); err != nil {
			return err
		}
		fn()
	}
}
