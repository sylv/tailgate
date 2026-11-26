// Package geo looks up an IP's country in DB-IP's lite country database,
// which is downloaded to the data dir and refreshed monthly
package geo

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

// db-ip publishes each month's file early on the 1st and numbers it by month
const url = "https://download.db-ip.com/free/dbip-country-lite-%s.mmdb.gz"

var errNotPublished = errors.New("not published yet")

type DB struct {
	path string
	mu   sync.RWMutex
	r    *maxminddb.Reader
	// err is why the last update failed
	err error
}

func Open(dir string) *DB {
	d := &DB{path: filepath.Join(dir, "dbip-country-lite.mmdb")}
	r, err := maxminddb.Open(d.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error("opening the geoip database failed", "error", err.Error())
	}
	d.r = r
	return d
}

// Country is the ISO 3166 code for ip, or "" when it's unknown
func (d *DB) Country(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.r == nil {
		return ""
	}
	var iso string
	d.r.Lookup(addr.Unmap()).DecodePath(&iso, "country", "iso_code")
	return iso
}

// Problem is the self check, "" when the database is there and less than 2 months old
func (d *DB) Problem() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	reason := ""
	if d.err != nil {
		reason = ": " + d.err.Error()
	}
	switch {
	case d.r == nil:
		return "The GeoIP database hasn't been downloaded, so country is empty for every request" + reason
	case time.Since(d.r.Metadata.BuildTime()) > 61*24*time.Hour:
		return "The GeoIP database is from " + d.r.Metadata.BuildTime().Format("January 2006") + " and couldn't be updated" + reason
	}
	return ""
}

// Update downloads this month's database when it's newer than the one on
// disk, then checks again every day
func (d *DB) Update(ctx context.Context) {
	for {
		err := d.update(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("updating the geoip database failed", "error", err.Error())
		}
		d.mu.Lock()
		d.err = err
		d.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}

func (d *DB) built() time.Time {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.r == nil {
		return time.Time{}
	}
	return d.r.Metadata.BuildTime()
}

func (d *DB) update(ctx context.Context) error {
	now := time.Now().UTC()
	this := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	// last month's is the fallback until this month's is published
	for _, month := range []time.Time{this, this.AddDate(0, -1, 0)} {
		if !d.built().Before(month) {
			return nil
		}
		if err := d.download(ctx, month.Format("2006-01")); !errors.Is(err, errNotPublished) {
			return err
		}
	}
	return nil
}

func (d *DB) download(ctx context.Context, month string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf(url, month), nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return errNotPublished
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s got %s", month, res.Status)
	}
	gz, err := gzip.NewReader(res.Body)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(d.path), "dbip-*.mmdb")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = io.Copy(tmp, gz)
	if err = errors.Join(err, tmp.Close()); err != nil {
		return err
	}
	r, err := maxminddb.Open(tmp.Name())
	if err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), d.path); err != nil {
		r.Close()
		return err
	}
	d.mu.Lock()
	old := d.r
	d.r = r
	d.mu.Unlock()
	if old != nil {
		old.Close()
	}
	slog.Info("updated the geoip database", "month", month)
	return nil
}
