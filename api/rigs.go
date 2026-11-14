// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wittywilla/wittycouch/gamestream"
	"github.com/wittywilla/wittymodule/host"
	"github.com/wittywilla/wittymodule/service"
)

const (
	rigStatusLife = 5 * time.Second
	rigSyncEvery  = 10 * time.Minute
	rigWakeEvery  = 10 * time.Second
	rigMaxApps    = 500
	// maxAppArt: a game's picture from a PC, which has no use for more.
	maxAppArt = 2 << 20
)

type rigCached struct {
	at   time.Time
	view rigState
}

// rigStarter is who started what from here.
type rigStarter struct {
	user int64
	app  int
}

func (r rigStarter) owns(user int64, app int) bool {
	return r.user != 0 && r.user == user && r.app == app
}

type rigPairing struct {
	PIN     string    `json:"pin,omitempty"`
	State   string    `json:"state"` // waiting, paired, failed
	Message string    `json:"message,omitempty"`
	Started time.Time `json:"-"`
	Kind    string    `json:"kind"`
	cancel  context.CancelFunc
}

// Stop ends what's running for rigs; the server is going away. Games on the PCs themselves
// carry on, as they're someone's to end.
func (a *App) Stop() {
	a.mu.Lock()
	a.stopped = true
	for _, p := range a.pairs {
		if p.cancel != nil {
			p.cancel()
		}
	}
	list := make([]*stream, 0, len(a.streams))
	for _, st := range a.streams {
		list = append(list, st)
	}
	a.mu.Unlock()
	for _, st := range list {
		st.Session.Close()
	}
}

// rigRow is a rig as it's stored. It has no JSON form: rigView decides what's shown to whom.
type rigRow struct {
	ID, LibraryID   int64
	Name, Address   string
	Port            int
	RemoteAddress   string
	AllowPublic     bool
	UUID, MAC, Cert string
	PairedAt        *int64
}

const rigCols = `id, library_id, name, address, port, remote_address, allow_public, uuid, mac, cert, paired_at`

func scanRig(row interface{ Scan(...any) error }) (*rigRow, error) {
	var r rigRow
	if err := row.Scan(&r.ID, &r.LibraryID, &r.Name, &r.Address, &r.Port, &r.RemoteAddress, &r.AllowPublic, &r.UUID, &r.MAC, &r.Cert, &r.PairedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

func (a *App) rigRows(ctx context.Context) ([]*rigRow, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT `+rigCols+` FROM rigs ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*rigRow
	for rows.Next() {
		r, err := scanRig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (a *App) rigByID(ctx context.Context, id int64) (*rigRow, error) {
	return scanRig(a.DB.QueryRowContext(ctx, `SELECT `+rigCols+` FROM rigs WHERE id = ?`, id))
}

// rigForRequest loads the rig in the path. Anyone not allowed to see it gets "not found".
func (a *App) rigForRequest(w http.ResponseWriter, r *http.Request) *rigRow {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return nil
	}
	rig, err := a.rigByID(r.Context(), id)
	if err != nil || !a.rigVisible(r.Context(), service.Who(r), rig) {
		writeErr(w, 404, "not found")
		return nil
	}
	return rig
}

// rigVisible: someone can see a rig if they can see at least one of its games, by the server's
// own rules. A Kids profile with nothing unlocked doesn't learn that it exists.
func (a *App) rigVisible(ctx context.Context, who service.Identity, rig *rigRow) bool {
	if who.Admin {
		return true
	}
	if who.User <= 0 {
		return false
	}
	items, err := a.Host.Items(ctx, host.On{User: who.User, Profile: who.Profile}, host.Query{Library: rig.LibraryID, Limit: 1})
	return err == nil && len(items) > 0
}

// rigClient makes the protocol client for a rig. The connection policy is applied to every
// connection it makes, whoever's request caused it.
func (a *App) rigClient(rig *rigRow) (*gamestream.Rig, error) {
	id, err := a.ident()
	if err != nil {
		return nil, err
	}
	return &gamestream.Rig{Address: rig.Address, Port: rig.Port, Identity: id, Cert: []byte(rig.Cert), Policy: gamestream.Policy{AllowPublic: rig.AllowPublic}}, nil
}

// rigState is how a rig is doing, in words for people.
type rigState struct {
	State       string `json:"state"` // online, busy, asleep, no-sunshine, not-paired, cert-changed, error
	Message     string `json:"message"`
	CurrentGame string `json:"currentGame,omitempty"`
	currentApp  int
	version     string
}

func statusFor(paired bool, info *gamestream.Info, err error) rigState {
	switch {
	case err == nil && !paired:
		return rigState{State: "not-paired", Message: "This PC is on, and needs pairing.", version: info.Version}
	case err == nil && info.CurrentGame > 0:
		return rigState{State: "busy", Message: "A game is running on this PC.", currentApp: info.CurrentGame, version: info.Version}
	case err == nil:
		return rigState{State: "online", Message: "Ready to play.", version: info.Version}
	case errors.Is(err, gamestream.ErrRefused):
		return rigState{State: "no-sunshine", Message: "This PC is on, but Sunshine isn't running on it. Start Sunshine on that PC."}
	case errors.Is(err, gamestream.ErrNoAnswer):
		return rigState{State: "asleep", Message: "This PC isn't answering. It's asleep, turned off, or not on this network."}
	case errors.Is(err, gamestream.ErrNotPaired):
		return rigState{State: "not-paired", Message: "This PC no longer trusts this server, so it has to be paired again."}
	case errors.Is(err, gamestream.ErrCertChanged):
		return rigState{State: "cert-changed", Message: "Something other than the PC you paired with answered at that address, so it wasn't trusted. If you reinstalled Sunshine, pair it again. If not, check what's at that address."}
	case errors.Is(err, gamestream.ErrBlocked):
		return rigState{State: "error", Message: "This PC's address isn't allowed: " + err.Error()}
	}
	return rigState{State: "error", Message: "This PC answered with something that isn't from Sunshine."}
}

// rigStatus asks a rig how it is, at most every few seconds however many people are looking.
func (a *App) rigStatus(ctx context.Context, rig *rigRow) rigState {
	a.mu.Lock()
	c, ok := a.statuses[rig.ID]
	a.mu.Unlock()
	if ok && time.Since(c.at) < rigStatusLife {
		return c.view
	}
	return a.rigStatusNow(ctx, rig)
}

func (a *App) rigStatusNow(ctx context.Context, rig *rigRow) rigState {
	var view rigState
	c, err := a.rigClient(rig)
	if err != nil {
		view = rigState{State: "error", Message: err.Error()}
	} else if rig.Cert == "" {
		info, err := c.Hello(ctx)
		view = statusFor(false, info, err)
	} else {
		info, err := c.Status(ctx)
		view = statusFor(true, info, err)
		// The PC only says its MAC address to someone it trusts, and it's what wakes it.
		if err == nil && info.MAC != rig.MAC {
			if _, merr := gamestream.ParseMAC(info.MAC); merr == nil {
				_, _ = a.DB.ExecContext(ctx, `UPDATE rigs SET mac = ? WHERE id = ?`, info.MAC, rig.ID)
				rig.MAC = info.MAC
			}
		}
	}
	if view.State == "online" && view.currentApp == 0 {
		// Nothing's running, so nothing is anyone's any more: what's started at the PC next isn't theirs.
		a.mu.Lock()
		delete(a.starter, rig.ID)
		a.mu.Unlock()
	}
	if view.currentApp > 0 {
		var title string
		if err := a.DB.QueryRowContext(ctx, `SELECT title FROM apps WHERE rig_id = ? AND app_id = ?`, rig.ID, view.currentApp).Scan(&title); err == nil {
			view.CurrentGame = title
		}
	}
	a.mu.Lock()
	a.statuses[rig.ID] = rigCached{at: time.Now(), view: view}
	a.mu.Unlock()
	return view
}

func (a *App) forgetRigStatus(id int64) {
	a.mu.Lock()
	delete(a.statuses, id)
	a.mu.Unlock()
}

// rigView is a rig as the API shows it. Where it is and how it's identified are for admins.
type rigView struct {
	ID          int64  `json:"id"`
	LibraryID   int64  `json:"libraryId"`
	Name        string `json:"name"`
	Paired      bool   `json:"paired"`
	State       string `json:"state"`
	Message     string `json:"message"`
	CurrentGame string `json:"currentGame,omitempty"`
	CanWake     bool   `json:"canWake"`
	CanQuit     bool   `json:"canQuit"`
	// Admins only.
	Address       string `json:"address,omitempty"`
	Port          int    `json:"port,omitempty"`
	RemoteAddress string `json:"remoteAddress,omitempty"`
	Version       string `json:"version,omitempty"`
	Fingerprint   string `json:"fingerprint,omitempty"`
	HasMAC        bool   `json:"hasMac,omitempty"`
	Public        bool   `json:"public,omitempty"`
	WebURL        string `json:"webUrl,omitempty"`
}

func (a *App) rigViewFor(ctx context.Context, who service.Identity, rig *rigRow) rigView {
	st := a.rigStatus(ctx, rig)
	a.mu.Lock()
	startedBy := a.starter[rig.ID]
	a.mu.Unlock()
	v := rigView{ID: rig.ID, LibraryID: rig.LibraryID, Name: rig.Name, Paired: rig.Cert != "", State: st.State, Message: st.Message, CurrentGame: st.CurrentGame,
		CanWake: rig.MAC != "" && (st.State == "asleep"), CanQuit: st.State == "busy" && (who.Admin || startedBy.owns(who.User, st.currentApp))}
	if !who.Admin {
		if st.State == "cert-changed" || st.State == "not-paired" || st.State == "error" {
			v.Message = "Something needs sorting out on this PC. Ask whoever runs this server."
		}
		return v
	}
	v.Address, v.Port, v.RemoteAddress, v.Version, v.HasMAC, v.Public = rig.Address, rig.Port, rig.RemoteAddress, st.version, rig.MAC != "", rig.AllowPublic
	if rig.Cert != "" {
		v.Fingerprint = gamestream.CertFingerprint([]byte(rig.Cert))
	}
	if c, err := a.rigClient(rig); err == nil {
		v.WebURL = c.WebURL()
	}
	return v
}

// visibleRigs are the rigs this person can see, and how they're doing; seen ones are synced now
// and then.
func (a *App) visibleRigs(ctx context.Context, who service.Identity) ([]rigView, error) {
	rows, err := a.rigRows(ctx)
	if err != nil {
		return nil, err
	}
	visible := rows[:0]
	for _, rig := range rows {
		if a.rigVisible(ctx, who, rig) {
			visible = append(visible, rig)
		}
	}
	out := make([]rigView, len(visible))
	var wg sync.WaitGroup
	for i, rig := range visible {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer recoverLog(a, "rigs: status")
			out[i] = a.rigViewFor(ctx, who, rig)
			if out[i].Paired && (out[i].State == "online" || out[i].State == "busy") {
				a.syncSoon(rig)
			}
		}()
	}
	wg.Wait()
	return out, nil
}

// GET /rigs: the rigs this account can see, and how they're doing.
func (a *App) handleRigs(w http.ResponseWriter, r *http.Request) {
	out, err := a.visibleRigs(r.Context(), service.Who(r))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, out)
}

func recoverLog(a *App, what string) {
	if v := recover(); v != nil {
		a.Log.Error(what, "panic", v)
	}
}

// foundRig is a PC that answered on the network.
type foundRig struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	Version string `json:"version"`
	Added   bool   `json:"added"`
}

// errFinding: someone else is looking already.
var errFinding = errors.New("Already looking. Try again in a moment.")

// findRigs looks for PCs running Sunshine on the home network. Takes a few seconds.
func (a *App) findRigs(ctx context.Context) ([]foundRig, error) {
	if !a.finding.TryLock() {
		return nil, errFinding
	}
	defer a.finding.Unlock()
	found, err := gamestream.Browse(ctx, 3*time.Second)
	if err != nil {
		return nil, err
	}
	rigs, err := a.rigRows(ctx)
	if err != nil {
		return nil, err
	}
	out := []foundRig{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, f := range found {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer recoverLog(a, "rigs: asking a PC")
			// mDNS can say anything, so it only counts if the PC itself answers.
			info, err := (&gamestream.Rig{Address: f.Address, Port: f.Port}).Hello(ctx)
			if err != nil {
				return
			}
			item := foundRig{Name: info.Name, Address: f.Address, Port: f.Port, Version: info.Version}
			for _, rig := range rigs {
				if rig.UUID != "" && strings.EqualFold(rig.UUID, info.UUID) {
					item.Added = true
					a.rigMoved(ctx, rig, f)
				}
			}
			mu.Lock()
			out = append(out, item)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out, nil
}

// GET /rigs/find: who on the home network runs Sunshine.
func (a *App) handleRigFind(w http.ResponseWriter, r *http.Request) {
	out, err := a.findRigs(r.Context())
	if errors.Is(err, errFinding) {
		writeErr(w, 409, err.Error())
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, out)
}

// rigMoved follows a paired PC to a new address, when its router gave it another. A name on
// the network proves nothing: only answering with the pinned certificate does.
func (a *App) rigMoved(ctx context.Context, rig *rigRow, f gamestream.Found) {
	if rig.Cert == "" || rig.Address == f.Address {
		return
	}
	id, err := a.ident()
	if err != nil {
		return
	}
	moved := &gamestream.Rig{Address: f.Address, Port: f.Port, Identity: id, Cert: []byte(rig.Cert)}
	if _, err := moved.Status(ctx); err != nil {
		return
	}
	if _, err := a.DB.ExecContext(ctx, `UPDATE rigs SET address = ?, port = ? WHERE id = ?`, f.Address, f.Port, rig.ID); err == nil {
		a.Log.Info("rig moved to a new address", "rig", rig.Name)
		a.forgetRigStatus(rig.ID)
	}
}

// addError is a refusal to add a PC, with its status.
type addError struct {
	code   int
	msg    string
	public bool
}

func (e *addError) Error() string { return e.msg }

// addRig adds a PC: it has to answer as Sunshine. Its library is made on the server.
func (a *App) addRig(ctx context.Context, address, name string, confirmPublic bool) (*rigRow, error) {
	h, port, err := gamestream.CleanAddress(address)
	if err != nil {
		return nil, &addError{code: 400, msg: err.Error()}
	}
	reach, err := gamestream.CheckAddress(ctx, h, nil)
	if err != nil {
		return nil, &addError{code: 400, msg: err.Error()}
	}
	if reach == gamestream.Public && !confirmPublic {
		return nil, &addError{code: 409, public: true, msg: "That address is on the internet, which means this PC's game streaming ports are open to everyone. A VPN is much safer: see the guide. Add it anyway only if you know that's what you want."}
	}
	probe := &gamestream.Rig{Address: h, Port: port, Policy: gamestream.Policy{AllowPublic: reach == gamestream.Public}}
	info, err := probe.Hello(ctx)
	if err != nil {
		code := 502
		msg := "That PC didn't answer. Check the address, that it's on, and that Sunshine is installed and running."
		switch {
		case errors.Is(err, gamestream.ErrRefused):
			msg = "That PC is on, but Sunshine isn't running on it. Install or start Sunshine, then try again."
		case errors.Is(err, gamestream.ErrBadAnswer):
			msg = "Something answered at that address, but it isn't Sunshine."
		case errors.Is(err, gamestream.ErrBlocked):
			msg, code = err.Error(), 400
		}
		return nil, &addError{code: code, msg: msg}
	}
	rows, err := a.rigRows(ctx)
	if err != nil {
		return nil, err
	}
	for _, other := range rows {
		if strings.EqualFold(other.UUID, info.UUID) {
			return nil, &addError{code: 409, msg: "That PC has already been added, as " + other.Name + "."}
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = info.Name
	}
	if name == "" {
		name = "Gaming PC"
	}
	if len(name) > 100 {
		name = name[:100]
	}
	lib, err := a.createLibrary(ctx, name)
	if err != nil {
		return nil, err
	}
	res, err := a.DB.ExecContext(ctx, `INSERT INTO rigs(library_id, name, address, port, allow_public, uuid, created_at) VALUES(?,?,?,?,?,?,?)`,
		lib.ID, name, h, port, reach == gamestream.Public, info.UUID, now())
	if err != nil {
		_ = a.deleteLibrary(ctx, lib.ID)
		return nil, err
	}
	id, _ := res.LastInsertId()
	return a.rigByID(ctx, id)
}

// POST /rigs {address, name, confirmPublic}: add a PC.
func (a *App) handleRigAdd(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Address       string `json:"address"`
		Name          string `json:"name"`
		ConfirmPublic bool   `json:"confirmPublic"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	row, err := a.addRig(r.Context(), b.Address, b.Name, b.ConfirmPublic)
	var ae *addError
	switch {
	case errors.As(err, &ae) && ae.public:
		writeJSON(w, ae.code, map[string]any{"public": true, "error": ae.msg})
		return
	case errors.As(err, &ae):
		writeErr(w, ae.code, ae.msg)
		return
	case err != nil:
		writeErr(w, 500, err.Error())
		return
	}
	a.Log.Info("rig added", "rig", row.Name, "by", service.Who(r).User)
	writeJSON(w, 201, a.rigViewFor(r.Context(), service.Who(r), row))
}

// PATCH /rigs/{id} {name, address, remoteAddress, mac}: only what's named changes. A new
// address keeps the pinned certificate, so it must still be the same PC there.
func (a *App) handleRigUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rig := a.rigForRequest(w, r)
	if rig == nil {
		return
	}
	var b struct {
		Name          *string `json:"name"`
		Address       *string `json:"address"`
		RemoteAddress *string `json:"remoteAddress"`
		MAC           *string `json:"mac"`
		ConfirmPublic bool    `json:"confirmPublic"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if b.Name != nil {
		name := strings.TrimSpace(*b.Name)
		if name == "" || len(name) > 100 {
			writeErr(w, 400, "A name is required, up to 100 characters.")
			return
		}
		if err := a.renameLibrary(ctx, rig.LibraryID, name); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		rig.Name = name
	}
	if b.Address != nil {
		h, port, err := gamestream.CleanAddress(*b.Address)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		reach, err := gamestream.CheckAddress(ctx, h, nil)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		if reach == gamestream.Public && !b.ConfirmPublic {
			writeJSON(w, 409, map[string]any{"public": true, "error": "That address is on the internet. A VPN is much safer: see the guide."})
			return
		}
		rig.Address, rig.Port, rig.AllowPublic = h, port, reach == gamestream.Public
	}
	if b.RemoteAddress != nil {
		rig.RemoteAddress = ""
		if v := strings.TrimSpace(*b.RemoteAddress); v != "" {
			h, _, err := gamestream.CleanAddress(v)
			if err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			rig.RemoteAddress = h
		}
	}
	if b.MAC != nil {
		rig.MAC = ""
		if v := strings.TrimSpace(*b.MAC); v != "" {
			hw, err := gamestream.ParseMAC(v)
			if err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			rig.MAC = hw.String()
		}
	}
	if _, err := a.DB.ExecContext(ctx, `UPDATE rigs SET name=?, address=?, port=?, remote_address=?, allow_public=?, mac=? WHERE id=?`,
		rig.Name, rig.Address, rig.Port, rig.RemoteAddress, rig.AllowPublic, rig.MAC, rig.ID); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.forgetRigStatus(rig.ID)
	writeJSON(w, 200, a.rigViewFor(ctx, service.Who(r), rig))
}

// forgetRig drops everything held in memory for a rig.
func (a *App) forgetRig(rig *rigRow) {
	a.mu.Lock()
	if p := a.pairs[rig.ID]; p != nil && p.cancel != nil {
		p.cancel()
	}
	delete(a.pairs, rig.ID)
	delete(a.statuses, rig.ID)
	delete(a.lastSync, rig.ID)
	delete(a.lastWake, rig.ID)
	delete(a.starter, rig.ID)
	a.mu.Unlock()
}

// removeRig forgets a PC and its rows, and with library, the library the server keeps for it.
func (a *App) removeRig(ctx context.Context, rig *rigRow, library bool) error {
	a.forgetRig(rig)
	if library {
		if err := a.deleteLibrary(ctx, rig.LibraryID); err != nil {
			return err
		}
	}
	_, err := a.DB.ExecContext(ctx, `DELETE FROM rigs WHERE id = ?`, rig.ID)
	return err
}

// libraryGone is told a library was deleted on the server (library settings), in case it's a
// rig's: the rig goes with it.
func (a *App) libraryGone(ctx context.Context, libraryID int64) {
	if rig, err := scanRig(a.DB.QueryRowContext(ctx, `SELECT `+rigCols+` FROM rigs WHERE library_id = ?`, libraryID)); err == nil {
		_ = a.removeRig(ctx, rig, false)
	}
}

// DELETE /rigs/{id}: forget a PC, its library and its pictures. The PC keeps this server
// on its own list until someone removes it there, which the protocol leaves no way to do.
func (a *App) handleRigRemove(w http.ResponseWriter, r *http.Request) {
	rig := a.rigForRequest(w, r)
	if rig == nil {
		return
	}
	if err := a.removeRig(r.Context(), rig, true); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.Log.Info("rig removed", "rig", rig.Name, "by", service.Who(r).User)
	writeJSON(w, 200, map[string]any{"ok": true, "note": "To finish, remove " + gamestream.DeviceName + " from the paired devices in Sunshine on that PC."})
}

// startPairing starts pairing with a rig, or gives the one already waiting.
func (a *App) startPairing(rig *rigRow) (rigPairing, error) {
	pin, err := gamestream.NewPIN()
	if err != nil {
		return rigPairing{}, err
	}
	a.mu.Lock()
	if p := a.pairs[rig.ID]; p != nil && p.State == "waiting" {
		answer := *p
		a.mu.Unlock()
		return answer, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), gamestream.PairTimeout)
	p := &rigPairing{PIN: pin, State: "waiting", Started: time.Now(), Kind: "server", cancel: cancel}
	a.pairs[rig.ID] = p
	answer := *p // the pairing changes this from here on
	a.mu.Unlock()
	go func() {
		defer recoverLog(a, "pairing a game PC")
		a.runRigPair(ctx, cancel, rig, p, pin)
	}()
	return answer, nil
}

// POST /rigs/{id}/pair: start pairing. The answer has the PIN to type into Sunshine, and the
// pairing carries on until someone does, or the time runs out.
func (a *App) handleRigPair(w http.ResponseWriter, r *http.Request) {
	rig := a.rigForRequest(w, r)
	if rig == nil {
		return
	}
	p, err := a.startPairing(rig)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.Log.Info("rig pairing started", "rig", rig.Name, "by", service.Who(r).User)
	writeJSON(w, 200, &p)
}

func (a *App) runRigPair(ctx context.Context, cancel context.CancelFunc, rig *rigRow, p *rigPairing, pin string) {
	defer cancel()
	finish := func(state, msg string) {
		a.mu.Lock()
		p.State, p.Message, p.PIN = state, msg, ""
		a.mu.Unlock()
	}
	c, err := a.rigClient(rig)
	if err != nil {
		finish("failed", err.Error())
		return
	}
	c.Cert = nil
	cert, err := c.Pair(ctx, pin)
	if err != nil {
		finish("failed", pairMessage(err))
		a.Log.Info("rig pairing failed", "rig", rig.Name, "reason", pairMessage(err))
		return
	}
	// A PC that was already paired comes back as itself. If it's another one, the old
	// certificate going away is what the admin just agreed to by typing a PIN into it.
	bg := context.Background()
	if _, err := a.DB.ExecContext(bg, `UPDATE rigs SET cert = ?, paired_at = ? WHERE id = ?`, string(cert), now(), rig.ID); err != nil {
		finish("failed", err.Error())
		return
	}
	rig.Cert = string(cert)
	a.forgetRigStatus(rig.ID)
	a.rigStatusNow(bg, rig)
	finish("paired", "Paired. Its games are being added to your library.")
	a.Log.Info("rig paired", "rig", rig.Name, "fingerprint", gamestream.CertFingerprint(cert))
	if _, err := a.syncRig(bg, rig); err != nil {
		a.Log.Warn("rig sync", "rig", rig.Name, "err", err)
	}
}

func pairMessage(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "Pairing was cancelled."
	case errors.Is(err, gamestream.ErrWrongPIN), errors.Is(err, gamestream.ErrPairTimedOut), errors.Is(err, gamestream.ErrAlreadyPairing),
		errors.Is(err, gamestream.ErrImposter), errors.Is(err, gamestream.ErrTooOld):
		return err.Error() + "."
	}
	return "Pairing didn't finish: " + err.Error() + "."
}

func (a *App) pairingOf(id int64) rigPairing {
	a.mu.Lock()
	defer a.mu.Unlock()
	if p := a.pairs[id]; p != nil {
		return *p
	}
	return rigPairing{State: "none"}
}

// GET /rigs/{id}/pair: how pairing is going. The PIN stays visible while it's waiting.
func (a *App) handleRigPairStatus(w http.ResponseWriter, r *http.Request) {
	rig := a.rigForRequest(w, r)
	if rig == nil {
		return
	}
	p := a.pairingOf(rig.ID)
	writeJSON(w, 200, &p)
}

// DELETE /rigs/{id}/pair: stop waiting.
func (a *App) handleRigPairCancel(w http.ResponseWriter, r *http.Request) {
	rig := a.rigForRequest(w, r)
	if rig == nil {
		return
	}
	a.mu.Lock()
	if p := a.pairs[rig.ID]; p != nil && p.cancel != nil {
		p.cancel()
	}
	a.mu.Unlock()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// POST /rigs/{id}/devices {pin, device, username, password}: type a PIN into Sunshine for
// another device. Sunshine's sign-in is passed through once to the pinned certificate, and is
// never kept or logged, as it can run any command on that PC.
func (a *App) handleRigDevicePIN(w http.ResponseWriter, r *http.Request) {
	rig := a.rigForRequest(w, r)
	if rig == nil {
		return
	}
	var b struct {
		PIN      string `json:"pin"`
		Device   string `json:"device"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, "That request couldn't be read.")
		return
	}
	if rig.Cert == "" {
		writeErr(w, 409, "Pair this PC with the server first.")
		return
	}
	c, err := a.rigClient(rig)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	err = c.SubmitPIN(r.Context(), b.Username, b.Password, b.PIN, b.Device)
	b.Password = ""
	if err != nil {
		code := 400
		if errors.Is(err, gamestream.ErrNoAnswer) || errors.Is(err, gamestream.ErrRefused) {
			code = 502
		}
		writeErr(w, code, err.Error())
		return
	}
	a.Log.Info("a device was paired with a rig", "rig", rig.Name, "by", service.Who(r).User)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// wake sends the wake-up packet, only to a registered rig's own MAC address. From here it even
// works while someone is away from home, as this server is on the rig's network.
func (a *App) wake(rig *rigRow) (string, error) {
	if rig.MAC == "" {
		return "", &addError{code: 409, msg: "This PC hasn't told the server how to wake it yet. Turn it on by hand once while it's paired, then this will work."}
	}
	a.mu.Lock()
	if time.Since(a.lastWake[rig.ID]) < rigWakeEvery {
		a.mu.Unlock()
		return "", nil
	}
	a.lastWake[rig.ID] = time.Now()
	delete(a.statuses, rig.ID)
	a.mu.Unlock()
	if err := gamestream.Wake(rig.MAC, rig.Address); err != nil {
		return "", &addError{code: 500, msg: "The wake-up signal couldn't be sent: " + err.Error()}
	}
	return "A wake-up signal was sent. A PC usually takes under a minute, if Wake-on-LAN is on in its settings.", nil
}

// POST /rigs/{id}/wake, for someone who can see that rig.
func (a *App) handleRigWake(w http.ResponseWriter, r *http.Request) {
	rig := a.rigForRequest(w, r)
	if rig == nil {
		return
	}
	note, err := a.wake(rig)
	var ae *addError
	if errors.As(err, &ae) {
		writeErr(w, ae.code, ae.msg)
		return
	}
	if note == "" {
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	a.Log.Info("rig woken", "rig", rig.Name, "by", service.Who(r).User)
	writeJSON(w, 200, map[string]any{"ok": true, "note": note})
}

// POST /rigs/{id}/quit: end what's running on the PC. For admins, and for whoever started it
// from here: ending someone else's game isn't something a viewer gets to do.
func (a *App) handleRigQuit(w http.ResponseWriter, r *http.Request) {
	rig := a.rigForRequest(w, r)
	if rig == nil {
		return
	}
	who := service.Who(r)
	if !who.Admin {
		// As it is now: the game they started may have ended, and another may be running.
		st := a.rigStatusNow(r.Context(), rig)
		a.mu.Lock()
		startedBy := a.starter[rig.ID]
		a.mu.Unlock()
		if !startedBy.owns(who.User, st.currentApp) {
			writeErr(w, 403, "Only whoever started it, or an admin, can end what's running on this PC.")
			return
		}
	}
	c, err := a.rigClient(rig)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := c.Quit(r.Context()); err != nil {
		writeErr(w, 502, statusFor(true, nil, err).Message)
		return
	}
	a.mu.Lock()
	delete(a.starter, rig.ID)
	delete(a.statuses, rig.ID)
	a.mu.Unlock()
	a.Log.Info("rig game ended", "rig", rig.Name, "by", who.User)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

var _ = sql.ErrNoRows
