package docker

import (
	"encoding/json"
	"maps"
	"testing"
)

// trimmed from a real /containers/json response
const listJSON = `[
	{"Id": "aaa", "Names": ["/immich"], "HostConfig": {"NetworkMode": "ryan_default"},
	 "NetworkSettings": {"Networks": {"zz_other": {"IPAddress": "172.20.0.9"}, "ryan_default": {"IPAddress": "172.18.0.5"}}}},
	{"Id": "bbb", "Names": ["/vpn"], "Labels": {"tailgate.network": "ryan_default"}, "HostConfig": {"NetworkMode": "ryan_default"},
	 "NetworkSettings": {"Networks": {"ryan_default": {"IPAddress": "172.18.0.4"}}}},
	{"Id": "ccc", "Names": ["/deluge"], "HostConfig": {"NetworkMode": "container:bbb"},
	 "NetworkSettings": {"Networks": {}}},
	{"Id": "ddd", "Names": ["/plex"], "HostConfig": {"NetworkMode": "host"},
	 "NetworkSettings": {"Networks": {"host": {"IPAddress": ""}}}},
	{"Id": "eee", "Names": ["/stopped"], "HostConfig": {"NetworkMode": "ryan_default"},
	 "NetworkSettings": {"Networks": {"ryan_default": {"IPAddress": ""}}}},
	{"Id": "fff", "Names": ["/orphan"], "HostConfig": {"NetworkMode": "container:gone"},
	 "NetworkSettings": {"Networks": {}}},
	{"Id": "ggg", "Names": ["/byname"], "HostConfig": {"NetworkMode": "container:vpn"},
	 "NetworkSettings": {"Networks": {}}}
]`

func TestResolve(t *testing.T) {
	var list []apiContainer
	if err := json.Unmarshal([]byte(listJSON), &list); err != nil {
		t.Fatal(err)
	}
	vpn := map[string]string{"ryan_default": "172.18.0.4"}
	want := map[string]Container{
		"immich":  {Networks: map[string]string{"zz_other": "172.20.0.9", "ryan_default": "172.18.0.5"}},
		"vpn":     {Networks: vpn},
		"deluge":  {Networks: vpn, NetworkLabels: map[string]string{"tailgate.network": "ryan_default"}},
		"plex":    {Host: "172.17.0.1"},
		"stopped": {Networks: map[string]string{"ryan_default": ""}},
		"orphan":  {},
		"byname":  {Networks: vpn, NetworkLabels: map[string]string{"tailgate.network": "ryan_default"}},
	}
	got := resolve(list, "172.17.0.1")
	if len(got) != len(want) {
		t.Fatalf("got %d containers", len(got))
	}
	for _, c := range got {
		w := want[c.Name]
		if !maps.Equal(c.Networks, w.Networks) || !maps.Equal(c.NetworkLabels, w.NetworkLabels) || c.Host != w.Host {
			t.Errorf("%s: got %+v, want %+v", c.Name, c, w)
		}
	}
}
