package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// events counts changes to anything the UI shows, so open pages load again
// when it moves instead of polling
type events struct {
	mu      sync.Mutex
	version int
	changed chan struct{}
}

func (e *events) bump() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.version++
	if e.changed != nil {
		close(e.changed)
		e.changed = nil
	}
}

// current returns the version and a channel closed when it moves on
func (e *events) current() (int, <-chan struct{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.changed == nil {
		e.changed = make(chan struct{})
	}
	return e.version, e.changed
}

// stream sends the version whenever it changes, and right away
func (g *gate) stream(w http.ResponseWriter, r *http.Request) {
	if requestOf(r.Context()).ident == nil {
		plain(w, r, http.StatusUnauthorized, "sign in required")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	rc := http.NewResponseController(w)
	for {
		v, changed := g.events.current()
		if _, err := fmt.Fprintf(w, "data: %d\n\n", v); err != nil || rc.Flush() != nil {
			return
		}
		select {
		case <-changed:
			// changes come in bursts, like a node going through its states, so wait for the rest
			time.Sleep(200 * time.Millisecond)
		case <-time.After(time.Minute):
			// proxies close streams that go quiet
		case <-r.Context().Done():
			return
		}
	}
}
