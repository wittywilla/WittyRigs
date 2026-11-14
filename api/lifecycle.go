// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/wittywilla/wittymodule"
	"github.com/wittywilla/wittymodule/host"
)

// Lifecycle answers the lifecycle calls integration.json lists. Taken off without its data kept,
// WittyRigs takes the libraries the server kept for its rigs too, then its own folder.
func Lifecycle(ctx context.Context, call string, args wittymodule.LifecycleArgs) error {
	if call != wittymodule.CallUninstall || args.KeepData {
		return nil
	}
	a, err := Get()
	if err != nil {
		return nil // nothing of its own to take
	}
	a.Stop()
	if rows, err := a.rigRows(ctx); err == nil {
		for _, rig := range rows {
			_ = a.deleteLibrary(ctx, rig.LibraryID)
		}
	}
	a.DB.Close()
	return os.RemoveAll(a.Dir)
}

var watchOnce sync.Once

// Watch follows the server's events while the service runs: a rig's library deleted in the
// library settings takes the rig with it, and an account that goes ends its streams.
func Watch() {
	watchOnce.Do(func() {
		go func() {
			var after int64
			first := true
			for {
				a, err := Get()
				if err != nil {
					time.Sleep(30 * time.Second)
					continue
				}
				if first {
					a.reconcile(context.Background())
					first = false
				}
				evs, next, err := a.Host.Events(context.Background(), after, 50*time.Second)
				if err != nil {
					time.Sleep(10 * time.Second)
					continue
				}
				after = next
				for _, e := range evs {
					a.onEvent(e)
				}
			}
		}()
	})
}

func (a *App) onEvent(e host.Event) {
	ctx := context.Background()
	switch e.Type {
	case "library.removed":
		var d struct {
			Library int64 `json:"library"`
		}
		if json.Unmarshal(e.Data, &d) == nil && d.Library > 0 {
			a.libraryGone(ctx, d.Library)
		}
	case "user.deleted":
		var d struct {
			User int64 `json:"user"`
		}
		if json.Unmarshal(e.Data, &d) == nil && d.User > 0 {
			a.endStreamsOf(d.User)
		}
	case "server.started":
		a.reconcile(ctx)
	}
}

// reconcile forgets rigs whose library went while this service wasn't listening.
func (a *App) reconcile(ctx context.Context) {
	libs, err := a.Host.Libraries(ctx)
	if err != nil || len(libs) == 0 {
		return // an empty answer could be anything; the event covers a library deleted while running
	}
	have := map[int64]bool{}
	for _, l := range libs {
		have[l.ID] = true
	}
	rows, err := a.rigRows(ctx)
	if err != nil {
		return
	}
	for _, rig := range rows {
		if !have[rig.LibraryID] {
			_ = a.removeRig(ctx, rig, false)
		}
	}
}
