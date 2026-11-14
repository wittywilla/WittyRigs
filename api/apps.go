// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wittywilla/wittycouch"
	"github.com/wittywilla/wittycouch/gamestream"
	"github.com/wittywilla/wittymodule/host"
	"github.com/wittywilla/wittymodule/service"
)

// desktopLike are apps that hand over the whole computer rather than a game. They stay out of
// the library until an admin says otherwise.
var desktopLike = regexp.MustCompile(`(?i)\b(desktop|terminal|powershell|command prompt|cmd|file explorer|task manager)\b`)

// rigApp is one app in an admin's list.
type rigApp struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Shown  bool   `json:"shown"`
	ItemID *int64 `json:"itemId"`
	art    string
}

func (a *App) rigAppList(ctx context.Context, rigID int64) ([]rigApp, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT app_id, title, shown, item_id, art FROM apps WHERE rig_id = ? ORDER BY title COLLATE NOCASE`, rigID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []rigApp{}
	for rows.Next() {
		var x rigApp
		var item sql.NullInt64
		if err := rows.Scan(&x.ID, &x.Title, &x.Shown, &item, &x.art); err != nil {
			return nil, err
		}
		if item.Valid {
			x.ItemID = &item.Int64
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// rigPath is the path of a rig's game: unique in the library, and naming nothing on disk.
func rigPath(rigID int64, appID int) string {
	return "rig://" + strconv.FormatInt(rigID, 10) + "/" + strconv.Itoa(appID)
}

// noArt is what an app's art is when the PC has only Sunshine's default picture for it.
const noArt = "none"

// syncRig brings a rig's library in line with what the PC offers. When the PC doesn't answer
// nothing changes, so a sleeping PC keeps its games. Art comes from the PC where it has any.
func (a *App) syncRig(ctx context.Context, rig *rigRow) ([]rigApp, error) {
	a.mu.Lock()
	lock := a.syncing[rig.ID]
	if lock == nil {
		lock = &sync.Mutex{}
		a.syncing[rig.ID] = lock
	}
	a.lastSync[rig.ID] = time.Now()
	a.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()

	c, err := a.rigClient(rig)
	if err != nil {
		return nil, err
	}
	apps, err := c.Apps(ctx)
	if err != nil {
		return nil, err
	}
	if len(apps) > rigMaxApps {
		apps = apps[:rigMaxApps]
	}
	known, err := a.rigAppList(ctx, rig.ID)
	if err != nil {
		return nil, err
	}
	before := map[int]rigApp{}
	for _, x := range known {
		before[x.ID] = x
	}
	// A picture shared by several apps is Sunshine's default: that's no art, and a tile with the
	// game's name looks better. A missing entry means the PC couldn't say, which changes nothing.
	pictures := map[int][]byte{}
	sums := map[[32]byte][]int{}
	for _, app := range apps {
		data, _, err := c.AppImage(ctx, app.ID)
		if err != nil || len(data) > maxAppArt {
			continue
		}
		pictures[app.ID] = data
		sum := sha256.Sum256(data)
		sums[sum] = append(sums[sum], app.ID)
	}
	art := map[int]string{}
	for sum, ids := range sums {
		for _, id := range ids {
			if len(ids) > 1 {
				art[id] = noArt
				delete(pictures, id)
			} else {
				art[id] = hex.EncodeToString(sum[:])
			}
		}
	}
	tagline := "Plays on " + rig.Name
	seen := map[int]bool{}
	for _, app := range apps {
		seen[app.ID] = true
		old, had := before[app.ID]
		shown := !desktopLike.MatchString(app.Title)
		if had {
			shown = old.Shown
		}
		var itemID any
		keptArt := old.art
		if shown {
			id, fresh, err := a.saveGame(ctx, rig.LibraryID, rigPath(rig.ID, app.ID), app.Title, tagline)
			if err != nil {
				return nil, err
			}
			itemID = id
			if sum, ok := art[app.ID]; ok && (fresh || sum != old.art) {
				if a.setPoster(ctx, id, pictures[app.ID]) == nil {
					keptArt = sum
				}
			}
		} else if had && old.ItemID != nil {
			a.removeGame(ctx, *old.ItemID)
			keptArt = ""
		}
		if _, err := a.DB.ExecContext(ctx, `INSERT INTO apps(rig_id, app_id, title, shown, item_id, art) VALUES(?,?,?,?,?,?)
			ON CONFLICT(rig_id, app_id) DO UPDATE SET title = excluded.title, shown = excluded.shown, item_id = excluded.item_id, art = excluded.art`,
			rig.ID, app.ID, app.Title, shown, itemID, keptArt); err != nil {
			return nil, err
		}
	}
	for _, old := range known {
		if seen[old.ID] {
			continue
		}
		if old.ItemID != nil {
			a.removeGame(ctx, *old.ItemID)
		}
		_, _ = a.DB.ExecContext(ctx, `DELETE FROM apps WHERE rig_id = ? AND app_id = ?`, rig.ID, old.ID)
	}
	return a.rigAppList(ctx, rig.ID)
}

// syncSoon refreshes a rig's games now and then, when someone is looking at them.
func (a *App) syncSoon(rig *rigRow) {
	a.mu.Lock()
	due := time.Since(a.lastSync[rig.ID]) > rigSyncEvery
	if due {
		a.lastSync[rig.ID] = time.Now()
	}
	a.mu.Unlock()
	if !due {
		return
	}
	copyOf := *rig
	go func() {
		defer recoverLog(a, "rigs: sync")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := a.syncRig(ctx, &copyOf); err != nil {
			a.Log.Debug("rig sync", "rig", copyOf.Name, "err", err)
		}
	}()
}

// POST /rigs/{id}/sync: read the PC's games again.
func (a *App) handleRigSync(w http.ResponseWriter, r *http.Request) {
	rig := a.rigForRequest(w, r)
	if rig == nil {
		return
	}
	apps, err := a.syncRig(r.Context(), rig)
	if err != nil {
		writeErr(w, 502, statusFor(rig.Cert != "", nil, err).Message)
		return
	}
	writeJSON(w, 200, apps)
}

// GET /rigs/{id}/apps: everything the PC offers, shown in the library or not.
func (a *App) handleRigApps(w http.ResponseWriter, r *http.Request) {
	rig := a.rigForRequest(w, r)
	if rig == nil {
		return
	}
	apps, err := a.rigAppList(r.Context(), rig.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, apps)
}

// errNoApp: the PC doesn't offer that app (as far as this server last heard).
var errNoApp = errors.New("not found")

// showApp puts an app in the library, or takes it out.
func (a *App) showApp(ctx context.Context, rig *rigRow, appID int, shown bool) error {
	var title string
	var item sql.NullInt64
	if err := a.DB.QueryRowContext(ctx, `SELECT title, item_id FROM apps WHERE rig_id = ? AND app_id = ?`, rig.ID, appID).Scan(&title, &item); err != nil {
		return errNoApp
	}
	var itemID any
	art := ""
	if shown {
		id, _, err := a.saveGame(ctx, rig.LibraryID, rigPath(rig.ID, appID), title, "Plays on "+rig.Name)
		if err != nil {
			return err
		}
		itemID = id
	} else if item.Valid {
		a.removeGame(ctx, item.Int64)
	}
	// Its picture comes with the next sync.
	if _, err := a.DB.ExecContext(ctx, `UPDATE apps SET shown = ?, item_id = ?, art = ? WHERE rig_id = ? AND app_id = ?`, shown, itemID, art, rig.ID, appID); err != nil {
		return err
	}
	if shown {
		a.mu.Lock()
		delete(a.lastSync, rig.ID)
		a.mu.Unlock()
		a.syncSoon(rig)
	}
	return nil
}

// PUT /rigs/{id}/apps/{app} {shown}: whether an app is in the library.
func (a *App) handleRigAppShown(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rig := a.rigForRequest(w, r)
	if rig == nil {
		return
	}
	appID, err := strconv.Atoi(r.PathValue("app"))
	if err != nil {
		writeErr(w, 400, "bad app")
		return
	}
	var b struct {
		Shown bool `json:"shown"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := a.showApp(ctx, rig, appID, b.Shown); errors.Is(err, errNoApp) {
		writeErr(w, 404, "not found")
		return
	} else if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	apps, err := a.rigAppList(ctx, rig.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, apps)
}

// ---------------------------------------------------------------- a rig's games

// rigGame is a game of a rig's that the person asking may see.
type rigGame struct {
	ID    int64
	Title string
}

// rigGameFor loads the game in the path, if this account may see it: the server checked that
// before passing the request on (the route's item), and the game has to be one of a rig's.
func (a *App) rigGameFor(w http.ResponseWriter, r *http.Request) (*rigGame, *rigRow, int) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return nil, nil, 0
	}
	who := service.Who(r)
	it := who.Item
	if it == nil || it.ID != id {
		got, err := a.Host.Item(r.Context(), host.On{User: who.User, Profile: who.Profile}, id)
		if err != nil {
			writeErr(w, 404, "not found")
			return nil, nil, 0
		}
		it = &got
	}
	if it.Kind != "" && it.Kind != "game" {
		writeErr(w, 404, "not found")
		return nil, nil, 0
	}
	rig, appID, err := a.rigOfItem(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "not found")
		return nil, nil, 0
	}
	return &rigGame{ID: id, Title: it.Title}, rig, appID
}

func (a *App) rigOfItem(ctx context.Context, itemID int64) (*rigRow, int, error) {
	var rigID int64
	var appID int
	if err := a.DB.QueryRowContext(ctx, `SELECT rig_id, app_id FROM apps WHERE item_id = ?`, itemID).Scan(&rigID, &appID); err != nil {
		return nil, 0, err
	}
	rig, err := a.rigByID(ctx, rigID)
	return rig, appID, err
}

// rigPlayView is everything a device needs to get to a game.
type rigPlayView struct {
	Rig     rigView             `json:"rig"`
	Game    rigPlayGame         `json:"game"`
	Away    bool                `json:"away"`
	Ready   bool                `json:"ready"`
	Message string              `json:"message,omitempty"`
	Methods []wittycouch.Method `json:"methods"`
	Install *wittycouch.Install `json:"install,omitempty"`
	Setup   []string            `json:"setup,omitempty"`
}

type rigPlayGame struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

var (
	rigClients = map[string]bool{"browser": true, "desktop": true, "android": true, "apple": true}
	rigSystems = map[string]bool{"windows": true, "mac": true, "linux": true, "chromeos": true, "android": true, "androidtv": true, "firetv": true, "ios": true, "tvos": true}
)

// Headers the server adds about where a request came from (module API 5).
const (
	// HeaderReached is the host (and port) the person reached the server by.
	HeaderReached = "X-Witty-Reached"
	// HeaderMachine is "1" when the request came from the server's own machine.
	HeaderMachine = "X-Witty-Machine"
)

// rigAddressFor is the address this person's Moonlight should use from where they are, or
// nothing. A PC that's the server itself is known here as this machine, which means nothing to
// anyone else, so they get the name they reached this server by.
func rigAddressFor(r *http.Request, rig *rigRow) (string, bool) {
	if service.Who(r).Away {
		return rig.RemoteAddress, true
	}
	if isLoopbackHost(rig.Address) && r.Header.Get(HeaderMachine) != "1" {
		reached := r.Header.Get(HeaderReached)
		h, _, err := net.SplitHostPort(reached)
		if err != nil {
			h = reached
		}
		if clean, _, err := gamestream.CleanAddress(h); err == nil {
			return clean, false
		}
		return "", false
	}
	return rig.Address, false
}

func isLoopbackHost(s string) bool {
	if s == "localhost" {
		return true
	}
	ip := net.ParseIP(s)
	return ip != nil && ip.IsLoopback()
}

// hasProgram says the Moonlight host program is there to play in the page.
func (a *App) hasProgram(ctx context.Context) bool {
	_, _, err := a.program(ctx)
	return err == nil
}

// POST /games/{id}/play {client, os}: what this device can do to play this game. It starts
// nothing on the PC. Playing in the page is offered if this server has its Moonlight program,
// and nobody else's game is running there.
func (a *App) handleRigPlay(w http.ResponseWriter, r *http.Request) {
	it, rig, appID := a.rigGameFor(w, r)
	if it == nil {
		return
	}
	var b wittycouch.Device
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if !rigClients[b.Client] {
		b.Client = "browser"
	}
	if !rigSystems[b.OS] {
		b.OS = ""
	}
	ctx := r.Context()
	who := service.Who(r)
	st := a.rigStatus(ctx, rig)
	addr, away := rigAddressFor(r, rig)
	t := wittycouch.Target{RigName: rig.Name, Address: addr, Port: rig.Port, UUID: rig.UUID, AppID: appID, AppName: it.Title, Away: away}
	view := rigPlayView{Rig: a.rigViewFor(ctx, who, rig), Game: rigPlayGame{ID: it.ID, Title: it.Title}, Away: away, Install: wittycouch.InstallFor(b.OS)}
	view.Ready = st.State == "online" || st.State == "busy"
	if st.State == "busy" && st.currentApp != appID {
		view.Ready = false
		name := "Another game"
		if st.CurrentGame != "" {
			name = st.CurrentGame
		}
		view.Message = name + " is running on this PC, so this one can't start until it's closed."
	} else if !view.Ready {
		view.Message = view.Rig.Message
	}
	if addr != "" && b.OS != "" {
		view.Setup = wittycouch.SetupSteps(b.OS, rig.Name, addr, rig.Port)
	}
	if away && addr == "" {
		view.Message = strings.TrimSpace(view.Message + " You're away from home, and Moonlight can't reach this PC from here unless you connect both to a private network first: see the guide.")
	}
	inPage := view.Ready && a.hasProgram(ctx) && a.rigMayJoin(rig, st, appID, who.User, who.Admin)
	view.Methods = wittycouch.Plan(b, t, inPage, "")
	if view.Methods == nil {
		view.Methods = []wittycouch.Method{}
	}
	writeJSON(w, 200, view)
}

// rigMayJoin: if something is running, a browser stream would join it rather than start
// anything, so that's only for whoever started it, or an admin.
func (a *App) rigMayJoin(rig *rigRow, st rigState, appID int, user int64, admin bool) bool {
	if st.currentApp == 0 {
		return true
	}
	if st.currentApp != appID {
		return false
	}
	if admin {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.starter[rig.ID].owns(user, appID)
}

// startError is why a game couldn't be started on a PC, with its status.
type startError struct {
	code int
	msg  string
}

func (e *startError) Error() string { return e.msg }

// startOnPC starts a game on the PC from here, so a device with no way to be handed a game (an
// Apple TV) can pick it up in Moonlight with Resume.
func (a *App) startOnPC(ctx context.Context, user int64, rig *rigRow, appID int, title string) ([]string, error) {
	c, err := a.rigClient(rig)
	if err != nil {
		return nil, err
	}
	if err := c.StartOnly(ctx, appID); err != nil {
		if errors.Is(err, gamestream.ErrBusy) {
			return nil, &startError{409, "Another game is running on this PC, so this one can't start until it's closed."}
		}
		var se *gamestream.StatusError
		if errors.As(err, &se) {
			return nil, &startError{502, "The PC couldn't start it. " + se.Message}
		}
		return nil, &startError{502, statusFor(true, nil, err).Message}
	}
	a.mu.Lock()
	a.starter[rig.ID] = rigStarter{user: user, app: appID}
	delete(a.statuses, rig.ID)
	a.mu.Unlock()
	a.Log.Info("rig game started", "rig", rig.Name, "game", title, "by", user)
	return []string{"Open the Moonlight app.", "Choose " + rig.Name + ".", "Choose " + title + ", then Resume."}, nil
}

// POST /games/{id}/start
func (a *App) handleRigStart(w http.ResponseWriter, r *http.Request) {
	it, rig, appID := a.rigGameFor(w, r)
	if it == nil {
		return
	}
	steps, err := a.startOnPC(r.Context(), service.Who(r).User, rig, appID, it.Title)
	var se *startError
	if errors.As(err, &se) {
		writeErr(w, se.code, se.msg)
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "steps": steps})
}

var _ = fmt.Sprint
