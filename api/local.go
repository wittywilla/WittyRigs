// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/wittywilla/wittycouch/gamestream"
	"github.com/wittywilla/wittymodule/service"
	"github.com/wittywilla/wittyrigs"
)

// This server's own Sunshine: the advanced option of hosting demanding games on this server's
// own hardware. It's off by default, and only ever looks at this machine.

const settingLocal = "localEnabled"

// LocalName is the name the rig for this machine's Sunshine gets, and its library.
const LocalName = "This server's games"

// localRig is the rig that stands for this machine, if there is one.
func (a *App) localRig(ctx context.Context) (*rigRow, error) {
	r, err := scanRig(a.DB.QueryRowContext(ctx, `SELECT `+rigCols+` FROM rigs WHERE address IN ('127.0.0.1', '::1', 'localhost') LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

// localStatus is what GET /local answers.
type localStatus struct {
	Enabled   bool     `json:"enabled"`
	Installed string   `json:"installed"`
	License   string   `json:"license"`
	Source    string   `json:"source"`
	Running   bool     `json:"running"`
	Rig       *rigView `json:"rig,omitempty"`
}

// findSunshine is where this machine's Sunshine is (tests stand in for it).
var findSunshine = wittyrigs.Find

func (a *App) localState(ctx context.Context, who service.Identity) localStatus {
	st := localStatus{Enabled: a.DB.Setting(ctx, settingLocal, "0") == "1", Installed: findSunshine(), License: wittyrigs.License, Source: wittyrigs.Source}
	if !st.Enabled {
		return st
	}
	hctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := (&gamestream.Rig{Address: "127.0.0.1"}).Hello(hctx)
	st.Running = err == nil
	// Added as soon as it can be, so the library is there once it's paired.
	if err == nil {
		if err := a.ensureLocalRig(ctx); err != nil {
			a.Log.Warn("wittyrigs", "err", err)
		}
	}
	if rig, err := a.localRig(ctx); err == nil && rig != nil {
		v := a.rigViewFor(ctx, who, rig)
		st.Rig = &v
	}
	return st
}

func (a *App) ensureLocalRig(ctx context.Context) error {
	if rig, err := a.localRig(ctx); err != nil || rig != nil {
		return err
	}
	info, err := (&gamestream.Rig{Address: "127.0.0.1"}).Hello(ctx)
	if err != nil {
		return err
	}
	lib, err := a.createLibrary(ctx, LocalName)
	if err != nil {
		return err
	}
	if _, err := a.DB.ExecContext(ctx, `INSERT INTO rigs(library_id, name, address, port, uuid, created_at) VALUES(?,?,?,?,?,?)`,
		lib.ID, LocalName, "127.0.0.1", 0, info.UUID, now()); err != nil {
		_ = a.deleteLibrary(ctx, lib.ID)
		return err
	}
	return nil
}

// setLocal turns this machine's Sunshine on or off. Turning it off takes this machine's library
// away too, as nothing should be left offering games from a host that's been turned off.
func (a *App) setLocal(ctx context.Context, on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	if err := a.DB.SetSetting(ctx, settingLocal, v); err != nil {
		return err
	}
	if !on {
		if rig, err := a.localRig(ctx); err == nil && rig != nil {
			return a.removeRig(ctx, rig, true)
		}
	}
	return nil
}

// GET /local
func (a *App) handleLocal(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.localState(r.Context(), service.Who(r)))
}

// PUT /local {enabled}
func (a *App) handleLocalSet(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := a.setLocal(r.Context(), b.Enabled); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.Log.Info("wittyrigs", "enabled", b.Enabled, "by", service.Who(r).User)
	writeJSON(w, 200, a.localState(r.Context(), service.Who(r)))
}
