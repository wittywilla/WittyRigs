// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wittywilla/wittycouch/gamestream/fake"
	"github.com/wittywilla/wittycouch/relay/fakehost"
	"github.com/wittywilla/wittymodule/host"
	"github.com/wittywilla/wittymodule/service"
)

// TestMain lets this test program stand in for the Moonlight host program, when a test starts it
// as one.
func TestMain(m *testing.M) {
	if os.Getenv(fakehost.Env) != "" {
		fakehost.Run()
		return
	}
	os.Exit(m.Run())
}

// fakeServer stands in for WittyMediaServer's host API: the libraries and items it keeps for
// WittyRigs, and who may see which (by the person's account).
type fakeServer struct {
	mu      sync.Mutex
	nextID  int64
	libs    map[int64]*fakeLib
	items   map[int64]*fakeItem
	program string
	// sees says which items an account may see: nil (or missing) is every one, for an adult.
	sees  map[int64][]int64
	kinds map[int64]string
}

type fakeLib struct {
	ID   int64
	Name string
}

type fakeItem struct {
	ID, Library   int64
	Path, Title   string
	Tagline       string
	Poster        string // "pc:<n bytes>", "hand", or ""
	PosterUploads int
}

func (f *fakeServer) visible(user int64, it *fakeItem) bool {
	switch f.kinds[user] {
	case "admin", "adult", "":
		return true
	}
	return slices.Contains(f.sees[user], it.ID)
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Witty-Host", "1")
	f.mu.Lock()
	defer f.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, host.Base)
	parts := strings.Split(strings.Trim(p, "/"), "/")
	user, _ := strconv.ParseInt(r.Header.Get(host.HeaderOnUser), 10, 64)
	answer := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	no := func(code int) { answer(code, map[string]string{"error": "no"}) }
	var body map[string]string
	if r.Header.Get("Content-Type") == "application/json" {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	id := int64(0)
	if len(parts) > 1 {
		id, _ = strconv.ParseInt(parts[1], 10, 64)
	}
	libJSON := func(l *fakeLib) map[string]any {
		return map[string]any{"id": l.ID, "name": l.Name, "type": "games", "console": "rig"}
	}
	switch {
	case r.Method == "POST" && p == "/libraries":
		f.nextID++
		l := &fakeLib{ID: f.nextID, Name: body["name"]}
		f.libs[l.ID] = l
		answer(201, libJSON(l))
	case r.Method == "GET" && p == "/libraries":
		var out []map[string]any
		for _, l := range f.libs {
			out = append(out, map[string]any{"id": l.ID, "name": l.Name, "kind": "games", "console": "rig"})
		}
		answer(200, map[string]any{"libraries": out})
	case parts[0] == "libraries" && len(parts) == 2 && r.Method == "PATCH":
		l := f.libs[id]
		if l == nil {
			no(404)
			return
		}
		l.Name = body["name"]
		answer(200, libJSON(l))
	case parts[0] == "libraries" && len(parts) == 2 && r.Method == "DELETE":
		if f.libs[id] == nil {
			no(404)
			return
		}
		delete(f.libs, id)
		for k, it := range f.items {
			if it.Library == id {
				delete(f.items, k)
			}
		}
		answer(200, map[string]bool{"ok": true})
	case parts[0] == "libraries" && len(parts) == 3 && parts[2] == "items" && r.Method == "PUT":
		if f.libs[id] == nil {
			no(404)
			return
		}
		for _, it := range f.items {
			if it.Library == id && it.Path == body["path"] {
				it.Title, it.Tagline = body["title"], body["tagline"]
				answer(200, map[string]any{"id": it.ID, "new": false})
				return
			}
		}
		f.nextID++
		it := &fakeItem{ID: f.nextID, Library: id, Path: body["path"], Title: body["title"], Tagline: body["tagline"]}
		f.items[it.ID] = it
		answer(200, map[string]any{"id": it.ID, "new": true})
	case parts[0] == "items" && len(parts) == 3 && parts[2] == "poster":
		it := f.items[id]
		if it == nil {
			no(404)
			return
		}
		if it.Poster != "hand" {
			if r.Method == "PUT" {
				b, _ := io.ReadAll(r.Body)
				it.Poster = fmt.Sprintf("pc:%d", len(b))
				it.PosterUploads++
			} else {
				it.Poster = ""
			}
		}
		answer(200, map[string]bool{"ok": true})
	case parts[0] == "items" && len(parts) == 2 && r.Method == "DELETE":
		delete(f.items, id)
		answer(200, map[string]bool{"ok": true})
	case parts[0] == "items" && len(parts) == 2 && r.Method == "GET":
		it := f.items[id]
		if it == nil {
			no(404)
			return
		}
		if user > 0 && !f.visible(user, it) {
			no(403)
			return
		}
		answer(200, service.Item{ID: it.ID, Library: it.Library, Kind: "game", Title: it.Title, Facets: map[string]string{"console": "rig"}})
	case r.Method == "GET" && p == "/items":
		lib, _ := strconv.ParseInt(r.URL.Query().Get("library"), 10, 64)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		var out []service.Item
		var ids []int64
		for k := range f.items {
			ids = append(ids, k)
		}
		slices.Sort(ids)
		for _, k := range ids {
			it := f.items[k]
			if it.Library == lib && (user == 0 || f.visible(user, it)) {
				out = append(out, service.Item{ID: it.ID, Library: it.Library, Kind: "game", Title: it.Title})
			}
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		answer(200, map[string]any{"items": out})
	case r.Method == "GET" && p == "/programs/wittycouchhost":
		if f.program == "" {
			no(404)
			return
		}
		answer(200, map[string]any{"path": f.program, "args": []string{}, "version": "1.0.0"})
	case r.Method == "GET" && p == "/events":
		answer(200, map[string]any{"events": []any{}, "next": 0})
	default:
		no(404)
	}
}

func (f *fakeServer) gamesIn(lib int64) []*fakeItem {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*fakeItem
	for _, it := range f.items {
		if it.Library == lib {
			c := *it
			out = append(out, &c)
		}
	}
	slices.SortFunc(out, func(a, b *fakeItem) int { return int(a.ID - b.ID) })
	return out
}

// rig is a test's WittyRigs with a stand-in server.
type rigEnv struct {
	t   *testing.T
	a   *App
	srv *fakeServer
	mux *http.ServeMux
}

func newEnv(t *testing.T) *rigEnv {
	t.Helper()
	t.Setenv("WMS_WITTYCOUCH_HOST", "")
	srv := &fakeServer{libs: map[int64]*fakeLib{}, items: map[int64]*fakeItem{}, sees: map[int64][]int64{},
		kinds: map[int64]string{1: "admin", 2: "adult", 3: "kids", 4: "guest", 5: "kids", 6: "adult"}}
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	dir := t.TempDir()
	db, err := OpenDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a := New(db, dir, host.NewAt(hs.URL, "token", nil))
	a.Log = slog.New(slog.DiscardHandler)
	Use(a)
	t.Cleanup(func() { a.Stop(); Use(nil) })
	mux := http.NewServeMux()
	Routes(mux)
	return &rigEnv{t: t, a: a, srv: srv, mux: mux}
}

// Accounts of the stand-in server.
const (
	admin = int64(1)
	adult = int64(2)
	kid   = int64(3)
	guest = int64(4)
	kidOK = int64(5) // a kid given one game
)

// do calls a route as the server would pass it on: who is asking, and for an item route, the
// item once the server has checked it may be seen.
func (e *rigEnv) do(method, path string, user int64, body any, hdr ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, rd)
	if user > 0 {
		r.Header.Set(service.HeaderUser, strconv.FormatInt(user, 10))
		if e.srv.kinds[user] == "admin" {
			r.Header.Set(service.HeaderAdmin, "1")
		}
	}
	r.Header.Set(HeaderReached, "example.com") // as the server says it, unless a test says otherwise
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	if strings.HasPrefix(path, "/games/") {
		id, _ := strconv.ParseInt(strings.Split(path, "/")[2], 10, 64)
		e.srv.mu.Lock()
		it := e.srv.items[id]
		ok := it != nil && e.srv.visible(user, it)
		e.srv.mu.Unlock()
		if !ok {
			w := httptest.NewRecorder()
			w.WriteHeader(404)
			return w
		}
		r.Header.Set(service.HeaderItem, service.EncodeItem(service.Item{ID: it.ID, Library: it.Library, Kind: "game", Title: it.Title}))
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("%d: %s: %v", w.Code, w.Body, err)
	}
	return v
}

func sunshine(t *testing.T) *fake.Sunshine {
	t.Helper()
	f, err := fake.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Close)
	return f
}

// pairedRig adds the fake as a rig through the API and pairs it.
func (e *rigEnv) pairedRig(f *fake.Sunshine) rigView {
	t := e.t
	t.Helper()
	w := e.do("POST", "/rigs", admin, map[string]any{"address": fmt.Sprintf("127.0.0.1:%d", f.Port())})
	if w.Code != 201 {
		t.Fatalf("add: %d %s", w.Code, w.Body)
	}
	v := decode[rigView](t, w)
	w = e.do("POST", fmt.Sprintf("/rigs/%d/pair", v.ID), admin, nil)
	p := decode[rigPairing](t, w)
	if p.State != "waiting" || len(p.PIN) != 4 {
		t.Fatalf("pairing: %+v", p)
	}
	if !f.WaitForPairing(5 * time.Second) {
		t.Fatal("nothing started pairing")
	}
	f.TypePIN(p.PIN)
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := decode[rigPairing](t, e.do("GET", fmt.Sprintf("/rigs/%d/pair", v.ID), admin, nil))
		if st.State == "paired" {
			break
		}
		if st.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("pairing: %+v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for time.Now().Before(deadline) {
		if apps := decode[[]rigApp](t, e.do("GET", fmt.Sprintf("/rigs/%d/apps", v.ID), admin, nil)); len(apps) == len(f.Apps) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return v
}

func gameNamed(t *testing.T, items []*fakeItem, title string) *fakeItem {
	t.Helper()
	for _, it := range items {
		if it.Title == title {
			return it
		}
	}
	t.Fatalf("no %q in %d games", title, len(items))
	return nil
}

// fakeHostProgram is a program that runs this test program as the stand-in host.
func fakeHostProgram(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in is a shell script")
	}
	p := filepath.Join(t.TempDir(), "wittycouch-host")
	script := "#!/bin/sh\n" + fakehost.Env + "=ok exec '" + self + "' \"$@\"\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

var _ = exec.Command
