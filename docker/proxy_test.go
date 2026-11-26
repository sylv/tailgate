package docker

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeDocker struct{ got []string }

func (f *fakeDocker) RoundTrip(r *http.Request) (*http.Response, error) {
	f.got = append(f.got, r.Method+" "+r.URL.RequestURI())
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("[]")), Header: http.Header{}, Request: r}, nil
}

func TestProxy(t *testing.T) {
	docker := &fakeDocker{}
	h := ProxyHandler(docker)
	for _, c := range []struct {
		method, target string
		ok             bool
	}{
		{"GET", "/containers/json?all=1", true},
		{"GET", "/networks/bridge", true},
		{"GET", `/events?filters={"type":["container"]}`, true},
		{"POST", "/containers/create", false},
		{"POST", "/containers/json", false},
		{"GET", "/containers/abc/json", false},
		{"GET", "/v1.47/containers/json", false},
		{"GET", "/containers/json/../create", false},
		{"DELETE", "/containers/abc", false},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, "http://docker"+c.target, nil))
		if (rec.Code == 200) != c.ok {
			t.Errorf("%s %s got %d", c.method, c.target, rec.Code)
		}
	}
	if len(docker.got) != 3 || docker.got[0] != "GET /containers/json?all=1" {
		t.Errorf("docker got %v", docker.got)
	}
}
