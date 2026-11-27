package service

import (
	"strings"
	"testing"

	"github.com/sylv/tailgate/docker"
)

func one(t *testing.T, labels map[string]string) Service {
	t.Helper()
	got := FromContainers([]docker.Container{{Name: "c1", Labels: labels}}, "")
	if len(got) != 1 {
		t.Fatalf("got %d services, want 1", len(got))
	}
	return got[0]
}

func TestValid(t *testing.T) {
	s := one(t, map[string]string{
		"tailgate.id":                       "photos",
		"tailgate.port":                     "2283",
		"tailgate.funnel":                   "true",
		"tailgate.dangerously-disable-auth": "false",
		"traefik.enable":                    "true",
	})
	want := Service{Container: "c1", ID: "photos", Hostname: "photos", Port: 2283, Funnel: true, Noindex: true}
	if s != want {
		t.Fatalf("got %+v, want %+v", s, want)
	}
}

func TestSpellings(t *testing.T) {
	for _, key := range []string{"tailgate.dangerously-disable-auth", "tailgate.dangerouslyDisableAuth", "tailgate.dangerously_disable_auth"} {
		s := one(t, map[string]string{"tailgate.id": "plex", "tailgate.port": "32400", key: "true"})
		if s.Err != nil || !s.DangerouslyDisableAuth {
			t.Errorf("%s: got %+v", key, s)
		}
	}
}

func TestDefaults(t *testing.T) {
	s := one(t, map[string]string{"tailgate.id": "photos", "tailgate.port": "2283"})
	if s.Err != nil || s.Funnel || s.DangerouslyDisableAuth || !s.Noindex {
		t.Fatalf("got %+v", s)
	}
	if s := one(t, map[string]string{"tailgate.id": "blog", "tailgate.port": "80", "tailgate.noindex": "false"}); s.Err != nil || s.Noindex {
		t.Fatalf("noindex=false got %+v", s)
	}
}

func TestNoLabels(t *testing.T) {
	got := FromContainers([]docker.Container{{Name: "c1", Labels: map[string]string{"traefik.enable": "true"}}, {Name: "c2"}}, "")
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestErrors(t *testing.T) {
	base := map[string]string{"tailgate.id": "photos", "tailgate.port": "2283"}
	with := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			if kv[i+1] == "<unset>" {
				delete(m, kv[i])
			} else {
				m[kv[i]] = kv[i+1]
			}
		}
		return m
	}
	cases := map[string]struct {
		labels map[string]string
		want   string
	}{
		"unknown key":         {with("tailgate.enable", "true"), "unknown label"},
		"wrong case":          {with("tailgate.Funnel", "true"), "unknown label"},
		"other spelling":      {with("tailgate.dangerously-disableAuth", "true"), "unknown label"},
		"funnel typo":         {with("tailgate.funnel", "yes"), "must be true or false"},
		"funnel capital":      {with("tailgate.funnel", "True"), "must be true or false"},
		"auth typo":           {with("tailgate.dangerously-disable-auth", "1"), "must be true or false"},
		"missing name":        {with("tailgate.id", "<unset>"), "tailgate.id is required"},
		"empty name":          {with("tailgate.id", ""), "tailgate.id is required"},
		"path in name":        {with("tailgate.id", "../etc"), "lowercase letters"},
		"uppercase name":      {with("tailgate.id", "Photos"), "lowercase letters"},
		"dotted name":         {with("tailgate.id", "photos.example"), "lowercase letters"},
		"missing port":        {with("tailgate.port", "<unset>"), "tailgate.port is required"},
		"port not a number":   {with("tailgate.port", "http"), "from 1 to 65535"},
		"port zero":           {with("tailgate.port", "0"), "from 1 to 65535"},
		"port too big":        {with("tailgate.port", "65536"), "from 1 to 65535"},
		"port with sign":      {with("tailgate.port", "+80"), "from 1 to 65535"},
		"empty network":       {with("tailgate.network", ""), "tailgate.network is empty"},
		"two spellings":       {with("tailgate.dangerously-disable-auth", "false", "tailgate.dangerouslyDisableAuth", "true"), "are the same key"},
		"only an unknown key": {map[string]string{"tailgate.enable": "true"}, "unknown label"},
	}
	for name, c := range cases {
		s := one(t, c.labels)
		if s.Err == nil || !strings.Contains(s.Err.Error(), c.want) {
			t.Errorf("%s: got error %v, want %q", name, s.Err, c.want)
		}
	}
}

func TestDuplicateNames(t *testing.T) {
	got := FromContainers([]docker.Container{
		{Name: "c1", Labels: map[string]string{"tailgate.id": "photos", "tailgate.port": "2283"}},
		{Name: "c2", Labels: map[string]string{"tailgate.id": "photos", "tailgate.port": "80"}},
		{Name: "c3", Labels: map[string]string{"tailgate.id": "plex", "tailgate.port": "32400"}},
		{Name: "c4", Labels: map[string]string{"tailgate.id": "plex-old", "tailgate.hostname": "plex", "tailgate.port": "32400"}},
		{Name: "c5", Labels: map[string]string{"tailgate.id": "blog-v2", "tailgate.hostname": "blog", "tailgate.port": "80"}},
	}, "")
	if len(got) != 5 {
		t.Fatalf("got %+v", got)
	}
	for _, s := range got {
		if (s.ID == "blog-v2") != (s.Err == nil) {
			t.Errorf("got %+v", s)
		}
	}
}

func TestNetwork(t *testing.T) {
	labels := func(kv ...string) map[string]string {
		m := map[string]string{"tailgate.id": "photos", "tailgate.port": "2283"}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	two := map[string]string{"ryan_proxy": "172.20.0.9", "ryan_default": "172.18.0.5"}
	cases := []struct {
		name     string
		c        docker.Container
		fallback string
		addr     string
		err      string
	}{
		{"first by name", docker.Container{Labels: labels(), Networks: two}, "", "172.18.0.5", ""},
		{"label", docker.Container{Labels: labels("tailgate.network", "ryan_proxy"), Networks: two}, "", "172.20.0.9", ""},
		{"label beats the default", docker.Container{Labels: labels("tailgate.network", "ryan_proxy"), Networks: two}, "ryan_default", "172.20.0.9", ""},
		{"default", docker.Container{Labels: labels(), Networks: two}, "ryan_proxy", "172.20.0.9", ""},
		{"shared container's label", docker.Container{Labels: labels(), Networks: two, NetworkLabels: map[string]string{"tailgate.network": "ryan_proxy"}}, "", "172.20.0.9", ""},
		{"own label beats the shared one", docker.Container{Labels: labels("tailgate.network", "ryan_default"), Networks: two, NetworkLabels: map[string]string{"tailgate.network": "ryan_proxy"}}, "", "172.18.0.5", ""},
		{"stopped", docker.Container{Labels: labels("tailgate.network", "ryan_proxy"), Networks: map[string]string{"ryan_proxy": ""}}, "", "", ""},
		{"host", docker.Container{Labels: labels(), Host: "172.17.0.1"}, "ryan_proxy", "172.17.0.1", ""},
		{"missing prefix", docker.Container{Labels: labels("tailgate.network", "proxy"), Networks: two}, "", "", `isn't on the "proxy" network, its networks are ryan_default, ryan_proxy`},
		{"default missing", docker.Container{Labels: labels(), Networks: two}, "proxy", "", `isn't on the "proxy" network`},
		{"no networks", docker.Container{Labels: labels()}, "proxy", "", "or any network"},
	}
	for _, c := range cases {
		got := FromContainers([]docker.Container{c.c}, c.fallback)[0]
		if got.Addr != c.addr || (c.err == "") != (got.Err == nil) || got.Err != nil && !strings.Contains(got.Err.Error(), c.err) {
			t.Errorf("%s: got %q, %v", c.name, got.Addr, got.Err)
		}
	}
}
