package node

import (
	"net/netip"
	"testing"
)

func TestRoutes(t *testing.T) {
	own := []netip.Addr{netip.MustParseAddr("100.64.0.7"), netip.MustParseAddr("fd7a:115c:a1e0::7")}
	plain := []netip.Prefix{netip.MustParsePrefix("100.64.0.7/32"), netip.MustParsePrefix("fd7a:115c:a1e0::7/128")}
	if routes(plain, own) {
		t.Error("a device's own addresses count as routes")
	}
	for _, extra := range []string{"192.168.1.0/24", "0.0.0.0/0", "100.64.0.8/32"} {
		if !routes(append(plain, netip.MustParsePrefix(extra)), own) {
			t.Errorf("%s isn't a route", extra)
		}
	}
}
