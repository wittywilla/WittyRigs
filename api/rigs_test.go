// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wittywilla/wittycouch/gamestream"
	"github.com/wittywilla/wittycouch/relay/fakehost"
	"github.com/wittywilla/wittymodule/host"
	"os/exec"
)

func TestRigFromAddingToPlaying(t *testing.T) {
	e := newEnv(t)
	f := sunshine(t)
	v := e.pairedRig(f)
	if v.Name != f.Name {
		t.Errorf("name %q", v.Name)
	}
	if lib := e.srv.libs[v.LibraryID]; lib == nil || lib.Name != f.Name {
		t.Fatalf("library %+v", lib)
	}

	// The library holds the games, and the whole-computer app stays out of it.
	items := e.srv.gamesIn(v.LibraryID)
	if len(items) != 2 {
		t.Fatalf("games: %d", len(items))
	}
	game := gameNamed(t, items, "Space Game")
	if game.Tagline != "Plays on "+f.Name || !strings.HasPrefix(game.Path, "rig://") {
		t.Errorf("game: %+v", game)
	}
	if !strings.HasPrefix(game.Poster, "pc:") {
		t.Error("the game with its own picture has none")
	}
	// All the default pictures are the same, which means no picture.
	if steam := gameNamed(t, items, "Steam Big Picture"); steam.Poster != "" {
		t.Errorf("default picture kept: %q", steam.Poster)
	}

	// Status, as the PC has told the server its MAC now.
	list := decode[[]rigView](t, e.do("GET", "/rigs", admin, nil))
	if len(list) != 1 || list[0].State != "online" || !list[0].Paired || !list[0].HasMAC || list[0].Fingerprint == "" {
		t.Fatalf("list: %+v", list)
	}

	// A plan for each kind of device, with what the Moonlight app needs.
	w := e.do("POST", fmt.Sprintf("/games/%d/play", game.ID), admin, map[string]any{"client": "desktop", "os": "windows"})
	if w.Code != 200 {
		t.Fatalf("play: %d %s", w.Code, w.Body)
	}
	plan := decode[rigPlayView](t, w)
	if !plan.Ready || len(plan.Methods) < 2 || plan.Methods[0].Kind != "command" || plan.Methods[0].Args[2] != "Space Game" || plan.Install == nil {
		t.Errorf("plan: %+v", plan)
	}
	plan = decode[rigPlayView](t, e.do("POST", fmt.Sprintf("/games/%d/play", game.ID), admin, map[string]any{"client": "android", "os": "androidtv"}))
	if plan.Methods[0].Kind != "intent" || plan.Methods[0].Extras["UUID"] != f.UUID || plan.Methods[0].Extras["AppId"] != "7" {
		t.Errorf("android plan: %+v", plan.Methods[0])
	}

	// Starting, seeing it running, refused a second time, and ended.
	if w := e.do("POST", fmt.Sprintf("/games/%d/start", game.ID), admin, nil); w.Code != 200 {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	if f.Running() != 7 {
		t.Errorf("running %d", f.Running())
	}
	list = decode[[]rigView](t, e.do("GET", "/rigs", admin, nil))
	if list[0].State != "busy" || list[0].CurrentGame != "Space Game" || !list[0].CanQuit {
		t.Errorf("while running: %+v", list[0])
	}
	steam := gameNamed(t, items, "Steam Big Picture")
	if w := e.do("POST", fmt.Sprintf("/games/%d/start", steam.ID), admin, nil); w.Code != 409 {
		t.Errorf("second game: %d", w.Code)
	}
	plan = decode[rigPlayView](t, e.do("POST", fmt.Sprintf("/games/%d/play", steam.ID), admin, map[string]any{"client": "browser"}))
	if plan.Ready || !strings.Contains(plan.Message, "Space Game") {
		t.Errorf("plan while another runs: %+v", plan)
	}
	if w := e.do("POST", fmt.Sprintf("/rigs/%d/quit", v.ID), admin, nil); w.Code != 200 {
		t.Errorf("quit: %d %s", w.Code, w.Body)
	}
	if f.Running() != 0 {
		t.Error("still running")
	}

	// Waking only goes to a registered rig.
	if w := e.do("POST", fmt.Sprintf("/rigs/%d/wake", v.ID), admin, nil); w.Code != 200 {
		t.Errorf("wake: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/rigs/99/wake", admin, nil); w.Code != 404 {
		t.Errorf("wake nothing: %d", w.Code)
	}

	// Showing and hiding.
	if w := e.do("PUT", fmt.Sprintf("/rigs/%d/apps/%d", v.ID, f.Apps[0].ID), admin, map[string]any{"shown": true}); w.Code != 200 {
		t.Errorf("show: %d %s", w.Code, w.Body)
	}
	if n := len(e.srv.gamesIn(v.LibraryID)); n != 3 {
		t.Errorf("after showing Desktop: %d", n)
	}
	// A game gone from the PC goes from the library; a PC that doesn't answer changes nothing.
	f.Apps = f.Apps[:2]
	if w := e.do("POST", fmt.Sprintf("/rigs/%d/sync", v.ID), admin, nil); w.Code != 200 {
		t.Errorf("sync: %d %s", w.Code, w.Body)
	}
	if n := len(e.srv.gamesIn(v.LibraryID)); n != 2 {
		t.Errorf("after a game left: %d", n)
	}
	f.Close()
	if w := e.do("POST", fmt.Sprintf("/rigs/%d/sync", v.ID), admin, nil); w.Code != 502 {
		t.Errorf("sync with it off: %d", w.Code)
	}
	if n := len(e.srv.gamesIn(v.LibraryID)); n != 2 {
		t.Errorf("a sleeping PC lost its games: %d", n)
	}
	e.a.forgetRigStatus(v.ID)
	if st := decode[[]rigView](t, e.do("GET", "/rigs", admin, nil))[0].State; st != "no-sunshine" {
		t.Errorf("state %q", st)
	}

	// Removing it takes the library and games with it.
	w = e.do("DELETE", fmt.Sprintf("/rigs/%d", v.ID), admin, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "paired devices") {
		t.Errorf("remove: %d %s", w.Code, w.Body)
	}
	if e.srv.libs[v.LibraryID] != nil {
		t.Error("its library is still there")
	}
	if rows, _ := e.a.rigRows(context.Background()); len(rows) != 0 {
		t.Errorf("rows: %d", len(rows))
	}
}

func TestRigPermissions(t *testing.T) {
	e := newEnv(t)
	f := sunshine(t)
	v := e.pairedRig(f)
	game := gameNamed(t, e.srv.gamesIn(v.LibraryID), "Space Game")
	e.srv.sees[kidOK] = []int64{game.ID}
	base := fmt.Sprintf("/rigs/%d", v.ID)

	// Anything that manages a rig or names an address is refused for everyone but admins.
	for _, who := range []int64{adult, kid, guest, kidOK, 0} {
		for _, c := range [][2]string{
			{"POST", "/rigs"}, {"GET", "/rigs/find"}, {"PATCH", base}, {"DELETE", base}, {"POST", base + "/pair"}, {"GET", base + "/pair"}, {"DELETE", base + "/pair"},
			{"POST", base + "/devices"}, {"POST", base + "/sync"}, {"GET", base + "/apps"}, {"PUT", base + fmt.Sprintf("/apps/%d", f.Apps[0].ID)},
			{"GET", "/local"}, {"PUT", "/local"},
		} {
			w := e.do(c[0], c[1], who, map[string]any{"address": "169.254.169.254", "shown": true, "enabled": true})
			if w.Code != 401 && w.Code != 403 {
				t.Errorf("%d %s %s: %d %s", who, c[0], c[1], w.Code, w.Body)
			}
		}
	}

	// Those without access can't learn it exists or reach it.
	for name, who := range map[string]int64{"kid": kid, "guest": guest} {
		if got := decode[[]rigView](t, e.do("GET", "/rigs", who, nil)); len(got) != 0 {
			t.Errorf("%s sees %+v", name, got)
		}
		if w := e.do("POST", base+"/wake", who, nil); w.Code != 404 {
			t.Errorf("%s wake: %d", name, w.Code)
		}
		if w := e.do("POST", base+"/quit", who, nil); w.Code != 404 {
			t.Errorf("%s quit: %d", name, w.Code)
		}
	}
	if launches, _ := f.Counts(); launches != 0 {
		t.Errorf("%d launches got through", launches)
	}

	// A viewer's list says nothing about where the PC is or how it's identified.
	w := e.do("GET", "/rigs", adult, nil)
	for _, secret := range []string{"127.0.0.1", f.MAC, "fingerprint", "webUrl", "BEGIN", "address"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("a viewer's list has %q: %s", secret, w.Body)
		}
	}
	if got := decode[[]rigView](t, w); len(got) != 1 {
		t.Errorf("adult sees %d rigs", len(got))
	}

	// What's unlocked works: the kid can start their game, and nobody else can end it.
	if w := e.do("POST", fmt.Sprintf("/games/%d/start", game.ID), kidOK, nil); w.Code != 200 {
		t.Fatalf("kid start: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", base+"/quit", adult, nil); w.Code != 403 {
		t.Errorf("another viewer quitting: %d", w.Code)
	}
	if w := e.do("POST", base+"/quit", kidOK, nil); w.Code != 200 {
		t.Errorf("own quit: %d %s", w.Code, w.Body)
	}
	// A game the server didn't pass on (it isn't one of a rig's) is nobody's here.
	e.srv.items[999] = &fakeItem{ID: 999, Library: 999, Title: "Other"}
	if w := e.do("POST", "/games/999/start", admin, nil); w.Code != 404 {
		t.Errorf("another library's game: %d", w.Code)
	}
}

func TestRigAddressesAreChecked(t *testing.T) {
	e := newEnv(t)
	for _, addr := range []string{"http://192.168.1.2", "192.168.1.2/admin", "user@pc", "169.254.169.254", "224.0.0.1", "0.0.0.0", "fe80::1", "pc?x=y", ""} {
		if w := e.do("POST", "/rigs", admin, map[string]any{"address": addr}); w.Code != 400 {
			t.Errorf("%q: %d %s", addr, w.Code, w.Body)
		}
	}
	// The internet needs saying twice.
	w := e.do("POST", "/rigs", admin, map[string]any{"address": "8.8.8.8"})
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"public":true`) {
		t.Errorf("public: %d %s", w.Code, w.Body)
	}
	if rows, _ := e.a.rigRows(context.Background()); len(rows) != 0 || len(e.srv.libs) != 0 {
		t.Errorf("something was stored: %d", len(rows))
	}
	f := sunshine(t)
	f.Garbage = true
	w = e.do("POST", "/rigs", admin, map[string]any{"address": fmt.Sprintf("127.0.0.1:%d", f.Port())})
	if w.Code != 502 || !strings.Contains(w.Body.String(), "isn't Sunshine") {
		t.Errorf("a printer: %d %s", w.Code, w.Body)
	}
	f.Garbage = false
	if w := e.do("POST", "/rigs", admin, map[string]any{"address": fmt.Sprintf("127.0.0.1:%d", f.Port())}); w.Code != 201 {
		t.Fatalf("add: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/rigs", admin, map[string]any{"address": fmt.Sprintf("localhost:%d", f.Port())}); w.Code != 409 {
		t.Errorf("twice: %d %s", w.Code, w.Body)
	}
	rows, _ := e.a.rigRows(context.Background())
	id := rows[0].ID
	if w := e.do("PATCH", fmt.Sprintf("/rigs/%d", id), admin, map[string]any{"mac": "ff:ff:ff:ff:ff:ff"}); w.Code != 400 {
		t.Errorf("group MAC: %d", w.Code)
	}
	if w := e.do("PATCH", fmt.Sprintf("/rigs/%d", id), admin, map[string]any{"address": "169.254.169.254"}); w.Code != 400 {
		t.Errorf("metadata address: %d", w.Code)
	}
	if w := e.do("PATCH", fmt.Sprintf("/rigs/%d", id), admin, map[string]any{"remoteAddress": "100.100.1.2", "name": "Den"}); w.Code != 200 {
		t.Errorf("VPN address: %d %s", w.Code, w.Body)
	}
	if e.srv.libs[rows[0].LibraryID].Name != "Den" {
		t.Error("the library wasn't renamed with it")
	}
}

func TestRigSecretsStayHome(t *testing.T) {
	e := newEnv(t)
	f := sunshine(t)
	v := e.pairedRig(f)
	id, err := e.a.ident()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/rigs", fmt.Sprintf("/rigs/%d/pair", v.ID), fmt.Sprintf("/rigs/%d/apps", v.ID), "/local"} {
		w := e.do("GET", p, admin, nil)
		if strings.Contains(w.Body.String(), "BEGIN") || strings.Contains(w.Body.String(), id.ID) {
			t.Errorf("%s has a secret: %s", p, w.Body)
		}
	}
	if st := decode[rigPairing](t, e.do("GET", fmt.Sprintf("/rigs/%d/pair", v.ID), admin, nil)); st.PIN != "" {
		t.Errorf("PIN still showing: %+v", st)
	}
	var n int
	if err := e.a.DB.QueryRow(`SELECT COUNT(*) FROM rigs WHERE cert LIKE '%PRIVATE%'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d %v", n, err)
	}
	// A rig answering with another certificate isn't trusted, and viewers get no detail.
	other := sunshine(t)
	if _, err := e.a.DB.Exec(`UPDATE rigs SET port = ? WHERE id = ?`, other.Port(), v.ID); err != nil {
		t.Fatal(err)
	}
	e.a.forgetRigStatus(v.ID)
	if got := decode[[]rigView](t, e.do("GET", "/rigs", admin, nil))[0]; got.State != "cert-changed" {
		t.Errorf("admin sees %+v", got)
	}
	if got := decode[[]rigView](t, e.do("GET", "/rigs", adult, nil))[0]; got.State != "cert-changed" || strings.Contains(got.Message, "certificate") {
		t.Errorf("viewer sees %+v", got)
	}
	if launches, _ := other.Counts(); launches != 0 {
		t.Error("a different PC was used")
	}
}

func TestRigDevicePINIsNeverStored(t *testing.T) {
	e := newEnv(t)
	f := sunshine(t)
	v := e.pairedRig(f)
	f.PendingDevice("Living Room TV")
	url := fmt.Sprintf("/rigs/%d/devices", v.ID)
	if w := e.do("POST", url, admin, map[string]any{"pin": "4321", "username": "admin", "password": "wrong"}); w.Code != 400 || !strings.Contains(w.Body.String(), "user name") {
		t.Errorf("wrong sign-in: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", url, admin, map[string]any{"pin": "4321", "username": "admin", "password": "secret"}); w.Code != 200 {
		t.Errorf("good: %d %s", w.Code, w.Body)
	}
	e.a.DB.Close()
	b, err := os.ReadFile(e.a.Dir + "/" + DBFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret") {
		t.Error("the database holds the password")
	}
}

func TestRigPairingRefusals(t *testing.T) {
	e := newEnv(t)
	f := sunshine(t)
	v := decode[rigView](t, e.do("POST", "/rigs", admin, map[string]any{"address": fmt.Sprintf("127.0.0.1:%d", f.Port())}))
	if v.State != "not-paired" {
		t.Errorf("state %q", v.State)
	}
	url := fmt.Sprintf("/rigs/%d/pair", v.ID)
	waitFor := func(state string) rigPairing {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			st := decode[rigPairing](t, e.do("GET", url, admin, nil))
			if st.State == state {
				return st
			}
			if time.Now().After(deadline) {
				t.Fatalf("pairing stuck at %+v", st)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	first := decode[rigPairing](t, e.do("POST", url, admin, nil))
	if again := decode[rigPairing](t, e.do("POST", url, admin, nil)); again.PIN != first.PIN {
		t.Errorf("a second PIN: %s, %s", first.PIN, again.PIN)
	}
	f.WaitForPairing(5 * time.Second)
	wrong := "0000"
	if first.PIN == wrong {
		wrong = "1111"
	}
	f.TypePIN(wrong)
	if st := waitFor("failed"); !strings.Contains(st.Message, "PIN") {
		t.Errorf("wrong PIN: %+v", st)
	}
	if rows, _ := e.a.rigRows(context.Background()); rows[0].Cert != "" {
		t.Error("a certificate was saved")
	}
	f.WrongCertInPairing = true
	p := decode[rigPairing](t, e.do("POST", url, admin, nil))
	f.WaitForPairing(5 * time.Second)
	f.TypePIN(p.PIN)
	if st := waitFor("failed"); !strings.Contains(st.Message, "pose") {
		t.Errorf("imposter: %+v", st)
	}
	if rows, _ := e.a.rigRows(context.Background()); rows[0].Cert != "" {
		t.Error("an imposter's certificate was saved")
	}
	f.WrongCertInPairing = false
	e.do("POST", url, admin, nil)
	f.WaitForPairing(5 * time.Second)
	e.do("DELETE", url, admin, nil)
	if st := waitFor("failed"); !strings.Contains(st.Message, "cancelled") {
		t.Errorf("cancel: %+v", st)
	}
}

func TestRigAddressDependsOnWhereYouAre(t *testing.T) {
	e := newEnv(t)
	f := sunshine(t)
	v := e.pairedRig(f)
	game := gameNamed(t, e.srv.gamesIn(v.LibraryID), "Space Game")
	url := fmt.Sprintf("/games/%d/play", game.ID)
	body := map[string]any{"client": "desktop", "os": "windows"}
	plan := func(hdr ...string) rigPlayView {
		t.Helper()
		w := e.do("POST", url, admin, body, hdr...)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		return decode[rigPlayView](t, w)
	}
	// A rig that's this very machine means nothing to another device on the network, which gets
	// the name it reached this server by.
	lan := plan(HeaderReached, "192.168.1.10:9797")
	if lan.Away || lan.Methods[0].Kind != "command" || !strings.HasPrefix(lan.Methods[0].Args[1], "192.168.1.10") {
		t.Errorf("at home: %+v", lan)
	}
	// On the server's own machine, it's this machine.
	if m := plan(HeaderReached, "localhost:9797", HeaderMachine, "1"); !strings.HasPrefix(m.Methods[0].Args[1], "127.0.0.1") {
		t.Errorf("on the machine: %+v", m.Methods[0])
	}
	// Away with no private address: there's nothing Moonlight could connect to, and it says so.
	away := plan(HeaderReached, "pc.example.ts.net", "X-Witty-Away", "1")
	if !away.Away || len(away.Methods) != 0 || !strings.Contains(away.Message, "away from home") {
		t.Errorf("away: %+v", away)
	}
	if w := e.do("PATCH", fmt.Sprintf("/rigs/%d", v.ID), admin, map[string]any{"remoteAddress": "100.100.1.2"}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	away = plan(HeaderReached, "pc.example.ts.net", "X-Witty-Away", "1")
	if !away.Away || len(away.Methods) == 0 || !strings.HasPrefix(away.Methods[0].Args[1], "100.100.1.2") {
		t.Errorf("away with a private address: %+v", away)
	}
}

func TestRigArtStaysWhenThePCCantSay(t *testing.T) {
	e := newEnv(t)
	f := sunshine(t)
	v := e.pairedRig(f)
	sync := func() {
		t.Helper()
		if w := e.do("POST", fmt.Sprintf("/rigs/%d/sync", v.ID), admin, nil); w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	game := func() *fakeItem { return gameNamed(t, e.srv.gamesIn(v.LibraryID), "Space Game") }
	before := game()
	if !strings.HasPrefix(before.Poster, "pc:") {
		t.Fatalf("poster %q", before.Poster)
	}
	f.NotImage = true
	sync()
	if g := game(); g.Poster != before.Poster {
		t.Errorf("a bad answer cost the game its picture: %q", g.Poster)
	}
	f.NotImage = false
	sync()
	// An unchanged picture isn't sent again.
	if g := game(); g.PosterUploads != 1 {
		t.Errorf("sent %d times", g.PosterUploads)
	}
}

func TestRigLibraryDeletedOnTheServer(t *testing.T) {
	e := newEnv(t)
	f := sunshine(t)
	v := e.pairedRig(f)
	e.a.onEvent(host.Event{Type: "library.removed", Data: []byte(fmt.Sprintf(`{"library":%d}`, v.LibraryID))})
	if rows, _ := e.a.rigRows(context.Background()); len(rows) != 0 {
		t.Errorf("%d rigs left", len(rows))
	}
	var n int
	if err := e.a.DB.QueryRow(`SELECT COUNT(*) FROM apps`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d apps left, %v", n, err)
	}
	// One gone while the service wasn't listening goes when it starts.
	f2 := sunshine(t)
	f2.UUID = "22222222-2222-3333-4444-555555555555"
	v2 := decode[rigView](t, e.do("POST", "/rigs", admin, map[string]any{"address": fmt.Sprintf("127.0.0.1:%d", f2.Port())}))
	e.srv.mu.Lock()
	delete(e.srv.libs, v2.LibraryID)
	e.srv.libs[999] = &fakeLib{ID: 999, Name: "another"}
	e.srv.mu.Unlock()
	e.a.reconcile(context.Background())
	if rows, _ := e.a.rigRows(context.Background()); len(rows) != 0 {
		t.Errorf("%d rigs left after reconciling", len(rows))
	}
}

func TestSunshineIsOffUntilAsked(t *testing.T) {
	e := newEnv(t)
	old := findSunshine
	findSunshine = func() string { return "" }
	t.Cleanup(func() { findSunshine = old })
	st := decode[localStatus](t, e.do("GET", "/local", admin, nil))
	if st.Enabled || st.License != "GPL-3.0" {
		t.Errorf("%+v", st)
	}
	if w := e.do("PUT", "/local", admin, map[string]any{"enabled": true}); w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Errorf("on: %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/local", admin, map[string]any{"enabled": false}); w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Errorf("off: %d %s", w.Code, w.Body)
	}
}

func TestRigStreamsThroughTheProgram(t *testing.T) {
	e := newEnv(t)
	f := sunshine(t)
	v := e.pairedRig(f)
	game := gameNamed(t, e.srv.gamesIn(v.LibraryID), "Space Game")
	play := fmt.Sprintf("/games/%d/stream", game.ID)

	// Without the program, it says so, and starts nothing.
	if w := e.do("POST", play, admin, map[string]any{}); w.Code != 503 || !strings.Contains(w.Body.String(), `"needProgram":true`) {
		t.Errorf("no program: %d %s", w.Code, w.Body)
	}
	if launches, _ := f.Counts(); launches != 0 {
		t.Error("a game was started with nothing to show it")
	}
	e.srv.program = fakeHostProgram(t)
	e.a.Prepare = func(c *exec.Cmd) { c.Env = append(c.Env, fakehost.Env+"=ok") }

	// Numbers from a browser are kept to something sensible on their way to a PC's encoder.
	w := e.do("POST", play, admin, map[string]any{"width": 99999, "height": 1, "fps": 100000, "bitrate": 1})
	if w.Code != 200 {
		t.Fatalf("stream: %d %s", w.Code, w.Body)
	}
	st := decode[sessionView](t, w)
	if st.ID == "" || st.Title != "Space Game" {
		t.Errorf("%+v", st)
	}
	if !strings.Contains(f.LastLaunch(), "mode=3840x360x120") {
		t.Errorf("sent %s", f.LastLaunch())
	}
	// The plan offers playing in the page now.
	plan := decode[rigPlayView](t, e.do("POST", fmt.Sprintf("/games/%d/play", game.ID), admin, map[string]any{"client": "browser"}))
	inPage := false
	for _, m := range plan.Methods {
		inPage = inPage || m.Kind == "client"
	}
	if !inPage {
		t.Errorf("no in-page method: %+v", plan.Methods)
	}
	// A stream is its owner's alone.
	url := "/sessions/" + st.ID
	for _, c := range [][2]string{{"GET", url}, {"POST", url + "/webrtc"}, {"DELETE", url}} {
		if w := e.do(c[0], c[1], adult, map[string]any{"sdp": "x"}); w.Code != 404 {
			t.Errorf("%s by another account: %d", c[0], w.Code)
		}
	}
	if w := e.do("GET", url, admin, nil); w.Code != 200 {
		t.Errorf("own stream: %d", w.Code)
	}
	if w := e.do("POST", url+"/webrtc", admin, map[string]any{"sdp": "not an offer"}); w.Code != 502 {
		t.Errorf("a bad offer: %d %s", w.Code, w.Body)
	}
	if !Busy() {
		t.Error("not busy while streaming")
	}
	// Ending it, and the game with it.
	if w := e.do("DELETE", url+"?quit=1", admin, nil); w.Code != 200 {
		t.Errorf("end: %d %s", w.Code, w.Body)
	}
	if f.Running() != 0 {
		t.Error("the game is still running")
	}
}

func TestSessionAddressIsRebuilt(t *testing.T) {
	good, err := cleanSessionURL("rtspenc://10.0.0.9:48010", "192.168.1.2")
	if err != nil || good != "rtspenc://192.168.1.2:48010" {
		t.Errorf("%q %v", good, err)
	}
	for _, bad := range []string{"", "http://x:48010", "rtsp://x", "rtsp://x:22", "rtsp://x:99999", "rtsp://x:48010/../..", "rtsp://x:48010?y=1", "file:///etc/passwd", "rtsp://x:48010\nHost: evil"} {
		if v, err := cleanSessionURL(bad, "192.168.1.2"); err == nil {
			t.Errorf("%q became %q", bad, v)
		}
	}
	ctx := context.Background()
	if _, err := resolveAllowed(ctx, "169.254.169.254", gamestream.Policy{AllowPublic: true}); err == nil {
		t.Error("the metadata address")
	}
	if _, err := resolveAllowed(ctx, "8.8.8.8", gamestream.Policy{}); err == nil {
		t.Error("the internet, when it isn't allowed")
	}
}
