// Package geo looks up an IP's country in DB-IP's lite country database,
// which the image build downloads, see the Dockerfile
package geo

import (
	"net/netip"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Path is where the image has the database
const Path = "/usr/share/tailgate/dbip-country-lite.mmdb"

type DB struct {
	r *maxminddb.Reader
}

func Open(path string) (*DB, error) {
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, err
	}
	return &DB{r: r}, nil
}

// Country is the ISO 3166 code for ip, or "" when it's unknown
func (d *DB) Country(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	var iso string
	d.r.Lookup(addr.Unmap()).DecodePath(&iso, "country", "iso_code")
	return iso
}
