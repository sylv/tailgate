package service

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/sylv/tailgate/docker"
)

const prefix = "tailgate."

// Service is one container with tailgate labels. A service with Err set
// isn't routed and has no node. The expr tags are the service.* values rules see
type Service struct {
	Container string `expr:"-"`
	Addr      string `expr:"-"`
	// ID is what tailgate keeps the service's grants, rules, sessions and node state under
	ID string `expr:"id" desc:"tailgate.id, \"\" for tailgate's own pages"`
	// Hostname is the node's name on the tailnet, so its domain
	Hostname               string `expr:"hostname" desc:"tailgate.hostname, or the id when it isn't set"`
	Port                   int    `expr:"port" desc:"tailgate.port"`
	Funnel                 bool   `expr:"funnel" desc:"tailgate.funnel"`
	DangerouslyDisableAuth bool   `expr:"dangerouslyDisableAuth" desc:"tailgate.dangerously-disable-auth"`
	Noindex                bool   `expr:"noindex" desc:"tailgate.noindex"`
	Network                string `expr:"network" desc:"tailgate.network, \"\" when it isn't set"`
	Err                    error  `expr:"-"`
}

// spellings maps every accepted spelling of a label key to its kebab case form
var spellings = map[string]string{}

func init() {
	for _, key := range []string{"id", "hostname", "port", "funnel", "dangerously-disable-auth", "noindex", "network"} {
		words := strings.Split(key, "-")
		camel := words[0]
		for _, w := range words[1:] {
			camel += strings.ToUpper(w[:1]) + w[1:]
		}
		spellings[key] = key
		spellings[camel] = key
		spellings[strings.Join(words, "_")] = key
	}
}

// the id names the node's state dir and is the default hostname, so both have to be plain dns labels
var validName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// FromContainers turns each container's labels into a service. Containers
// without tailgate labels are skipped. network is TAILGATE_NETWORK, the
// network to reach containers on when they don't say. The result is sorted by
// id, then container
func FromContainers(containers []docker.Container, network string) []Service {
	var services []Service
	// an id or hostname used twice breaks every container using it
	used := map[string][]int{}
	for _, c := range containers {
		s, ok := parse(c.Labels)
		if !ok {
			continue
		}
		s.Container = c.Name
		var err error
		if s.Addr, err = addr(c, cmp.Or(s.Network, networkLabel(c.NetworkLabels), network)); err != nil {
			s.Err = errors.Join(s.Err, err)
		}
		if s.ID != "" {
			used["id "+s.ID] = append(used["id "+s.ID], len(services))
		}
		if s.Hostname != "" {
			used["hostname "+s.Hostname] = append(used["hostname "+s.Hostname], len(services))
		}
		services = append(services, s)
	}
	for key, idx := range used {
		if len(idx) < 2 {
			continue
		}
		kind, value, _ := strings.Cut(key, " ")
		for _, i := range idx {
			services[i].Err = errors.Join(services[i].Err, fmt.Errorf("%d containers use the %s %q", len(idx), kind, value))
		}
	}
	sort.Slice(services, func(i, j int) bool {
		if services[i].ID != services[j].ID {
			return services[i].ID < services[j].ID
		}
		return services[i].Container < services[j].Container
	})
	return services
}

func parse(labels map[string]string) (Service, bool) {
	var s Service
	values := map[string]string{}
	seen := map[string]string{}
	var errs []error
	for label, value := range labels {
		raw, ok := strings.CutPrefix(label, prefix)
		if !ok {
			continue
		}
		key, ok := spellings[raw]
		if !ok {
			errs = append(errs, fmt.Errorf("unknown label %q", label))
			continue
		}
		if other, ok := seen[key]; ok {
			a, b := min(other, label), max(other, label)
			errs = append(errs, fmt.Errorf("labels %q and %q are the same key", a, b))
			continue
		}
		seen[key] = label
		values[key] = value
	}
	if len(seen) == 0 && len(errs) == 0 {
		return s, false
	}

	for _, key := range []string{"id", "hostname"} {
		v, ok := values[key]
		switch {
		case key == "hostname" && !ok:
			s.Hostname = s.ID
		case v == "":
			errs = append(errs, fmt.Errorf("tailgate.%s is required", key))
		case !validName.MatchString(v):
			errs = append(errs, fmt.Errorf("tailgate.%s %q must be lowercase letters, digits and dashes", key, v))
		case key == "id":
			s.ID = v
		default:
			s.Hostname = v
		}
	}

	if port, ok := values["port"]; !ok || port == "" {
		errs = append(errs, errors.New("tailgate.port is required"))
	} else if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
		errs = append(errs, fmt.Errorf("tailgate.port %q must be a number from 1 to 65535", port))
	} else {
		s.Port = int(n)
	}

	s.Noindex = true
	for key, dst := range map[string]*bool{"funnel": &s.Funnel, "dangerously-disable-auth": &s.DangerouslyDisableAuth, "noindex": &s.Noindex} {
		v, ok := values[key]
		if !ok {
			continue
		}
		switch v {
		case "true":
			*dst = true
		case "false":
			*dst = false
		default:
			errs = append(errs, fmt.Errorf("%s=%q must be true or false", seen[key], v))
		}
	}

	if v, ok := values["network"]; ok {
		if v == "" {
			errs = append(errs, fmt.Errorf("%s is empty", seen["network"]))
		}
		s.Network = v
	}

	if len(errs) > 0 {
		sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
		s.Err = errors.Join(errs...)
	}
	return s, true
}

// networkLabel is tailgate.network in any spelling, for the container another
// one shares its network with
func networkLabel(labels map[string]string) string {
	for label, value := range labels {
		if raw, ok := strings.CutPrefix(label, prefix); ok && spellings[raw] == "network" {
			return value
		}
	}
	return ""
}

// addr is the IP to reach c at on network, or on its first network by name
// when network is "". It's "" while the container is stopped
func addr(c docker.Container, network string) (string, error) {
	if c.Host != "" {
		return c.Host, nil
	}
	names := slices.Sorted(maps.Keys(c.Networks))
	if network == "" {
		if len(names) == 0 {
			return "", nil
		}
		return c.Networks[names[0]], nil
	}
	ip, ok := c.Networks[network]
	if !ok && len(names) == 0 {
		return "", fmt.Errorf("the container isn't on the %q network, or any network", network)
	}
	if !ok {
		// compose prefixes networks with the project name, which is the usual mistake
		return "", fmt.Errorf("the container isn't on the %q network, its networks are %s", network, strings.Join(names, ", "))
	}
	return ip, nil
}
