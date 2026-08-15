// Package testutil provides an in-process fake of the Ollama HTTP API. It
// depends only on the standard library so it can never pull a dependency into
// the production require block.
package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Fake is an httptest server speaking just enough of the Ollama API.
//
// Requests holds every decoded /api/generate body in call order; read it after
// the call under test has returned. len(Requests) == 0 is how a test asserts no
// generation happened.
type Fake struct {
	*httptest.Server

	mu       sync.Mutex
	Requests []map[string]any
}

// Count is a race-free len(Requests) for tests that inspect a fake while a
// request may still be in flight.
func (f *Fake) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Requests)
}

func (f *Fake) record(r *http.Request) {
	body := map[string]any{}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.Requests = append(f.Requests, body)
	f.mu.Unlock()
}

func newFake(t *testing.T, h http.HandlerFunc) *Fake {
	t.Helper()
	f := &Fake{}
	f.Server = httptest.NewServer(h)
	t.Cleanup(f.Server.Close)
	return f
}

// NewFake serves /api/tags with the given model names and answers each
// /api/generate call with the next reply, which is the string placed in the
// "response" field, i.e. the JSON the model "wrote". The final reply repeats
// once the list is exhausted.
func NewFake(t *testing.T, models []string, replies ...string) *Fake {
	t.Helper()
	var f *Fake
	f = newFake(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			writeTags(w, models)
		case "/api/generate":
			f.record(r)
			reply := ""
			if n := len(replies); n > 0 {
				i := f.Count() - 1
				if i >= n {
					i = n - 1
				}
				reply = replies[i]
			}
			writeJSON(w, http.StatusOK, map[string]any{"response": reply, "done": true, "done_reason": "stop"})
		default:
			http.NotFound(w, r)
		}
	})
	return f
}

// NewFakeStatus answers every endpoint with code and body. Use it for non-200
// paths and, with code 200, for malformed-body injection.
func NewFakeStatus(t *testing.T, code int, body string) *Fake {
	t.Helper()
	var f *Fake
	f = newFake(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/generate" {
			f.record(r)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	})
	return f
}

// NewFakeNDJSON answers /api/generate with one JSON object per line, as a
// streaming server or a chunking proxy would.
func NewFakeNDJSON(t *testing.T, chunks ...string) *Fake {
	t.Helper()
	var f *Fake
	f = newFake(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			writeTags(w, nil)
		case "/api/generate":
			f.record(r)
			w.Header().Set("Content-Type", "application/x-ndjson")
			enc := json.NewEncoder(w)
			for i, c := range chunks {
				_ = enc.Encode(map[string]any{"response": c, "done": i == len(chunks)-1})
			}
		default:
			http.NotFound(w, r)
		}
	})
	return f
}

// NewFakeSlow waits d before replying, or returns early when the client goes
// away, so a cancelled test never blocks the server's Close.
func NewFakeSlow(t *testing.T, d time.Duration) *Fake {
	t.Helper()
	var f *Fake
	f = newFake(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/generate" {
			f.record(r)
		}
		select {
		case <-time.After(d):
		case <-r.Context().Done():
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"response": "{}", "done": true})
	})
	return f
}

func writeTags(w http.ResponseWriter, models []string) {
	list := make([]map[string]any, 0, len(models))
	for _, m := range models {
		list = append(list, map[string]any{"name": m, "model": m})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": list})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
