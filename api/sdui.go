// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/wittywilla/wittymodule/host"
	"github.com/wittywilla/wittymodule/sdui"
	core "github.com/wittywilla/wittymodule/sdui/core"
	"github.com/wittywilla/wittymodule/service"
	"github.com/wittywilla/wittyrigs/ui"
)

// The sources behind WittyRigs' pages on every app (sdui.json): the rigs a person can see, one
// rig's rows, the PCs on the network and this server's own Sunshine. Every action is one of
// WittyRigs' own commands, which run its routes, and those check again who may.

// SDUIItems answers the server's sdui.items calls.
func SDUIItems(req sdui.ItemsRequest) (sdui.ItemsAnswer, error) {
	typ := req.Source.Type()
	if req.Viewer.User <= 0 || slices.Contains(req.Viewer.Cannot, "games") {
		return sdui.ItemsAnswer{}, nil
	}
	a, err := Get()
	if err != nil {
		return sdui.ItemsAnswer{}, err
	}
	return a.items(req, typ)
}

func (a *App) items(req sdui.ItemsRequest, typ string) (sdui.ItemsAnswer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	who := service.Identity{User: req.Viewer.User, Admin: req.Viewer.Admin}
	var all []sdui.Item
	switch typ {
	case ui.SourceRigs:
		views, err := a.visibleRigs(ctx, who)
		if err != nil {
			return sdui.ItemsAnswer{}, err
		}
		for _, v := range views {
			all = append(all, sdui.Item{ID: strconv.FormatInt(v.ID, 10), Kind: "view", Title: v.Name, Subtitle: rigLine(v), Trailing: stateLabel(v.State), Icon: "devices",
				Route: "/page/" + ui.PageRig + "?item=" + strconv.FormatInt(v.ID, 10) + "&name=" + url.QueryEscape(v.Name)})
		}
	case ui.SourceRig:
		id, _ := strconv.ParseInt(fmt.Sprint(req.Source["rig"]), 10, 64)
		rig, err := a.rigByID(ctx, id)
		if err != nil || !a.rigVisible(ctx, who, rig) {
			return sdui.ItemsAnswer{}, nil
		}
		switch fmt.Sprint(req.Source["part"]) {
		case "games":
			all, err = a.gameRows(ctx, who, rig)
		case "apps":
			if who.Admin {
				all, err = a.appRows(ctx, rig)
			}
		default:
			all = a.actionRows(ctx, who, rig)
		}
		if err != nil {
			return sdui.ItemsAnswer{}, err
		}
	case ui.SourceFound:
		if !who.Admin {
			return sdui.ItemsAnswer{}, nil
		}
		found, err := a.findRigs(ctx)
		if err != nil && err != errFinding {
			return sdui.ItemsAnswer{}, err
		}
		for _, f := range found {
			if f.Added {
				continue
			}
			addr := net.JoinHostPort(f.Address, strconv.Itoa(f.Port))
			all = append(all, sdui.Item{ID: addr, Kind: "view", Title: f.Name, Subtitle: "Sunshine " + f.Version + " at " + f.Address, Icon: "add",
				Action: &sdui.Action{Type: core.ActCommand, Op: ui.CommandAdd, Item: addr}})
		}
	case ui.SourceLocal:
		if !who.Admin {
			return sdui.ItemsAnswer{}, nil
		}
		all = a.localRows(ctx, who)
	default:
		return sdui.ItemsAnswer{}, nil
	}
	return page(all, req.Offset, req.Limit), nil
}

func page(all []sdui.Item, offset, limit int) sdui.ItemsAnswer {
	if offset < 0 || offset > len(all) {
		offset = len(all)
	}
	rest := all[offset:]
	if limit > 0 && len(rest) > limit {
		return sdui.ItemsAnswer{Items: rest[:limit], More: true, Total: len(all)}
	}
	return sdui.ItemsAnswer{Items: rest, Total: len(all)}
}

func rigLine(v rigView) string {
	if v.CurrentGame != "" {
		return v.CurrentGame + " is running."
	}
	return v.Message
}

// stateLabel is a rig's state in a word or two, for the end of its row.
func stateLabel(s string) string {
	switch s {
	case "online":
		return "Ready"
	case "busy":
		return "Playing"
	case "asleep":
		return "Asleep"
	case "no-sunshine":
		return "Sunshine off"
	case "not-paired":
		return "Not paired"
	}
	return "Needs a look"
}

func command(op, item string) *sdui.Action {
	return &sdui.Action{Type: core.ActCommand, Op: op, Item: item}
}

// actionRows are what can be done to a rig now.
func (a *App) actionRows(ctx context.Context, who service.Identity, rig *rigRow) []sdui.Item {
	v := a.rigViewFor(ctx, who, rig)
	id := strconv.FormatInt(rig.ID, 10)
	out := []sdui.Item{{ID: "state", Kind: "view", Title: stateLabel(v.State), Subtitle: rigLine(v), Icon: "info"}}
	if v.CanWake {
		out = append(out, sdui.Item{ID: "wake", Kind: "view", Title: "Wake it", Subtitle: "Sends " + rig.Name + " the wake-up signal. A PC usually takes under a minute.", Icon: "power",
			Action: command(ui.CommandWake, id)})
	}
	if v.CanQuit {
		act := command(ui.CommandQuit, id)
		act.Confirm = "End what's running on " + rig.Name + "?"
		out = append(out, sdui.Item{ID: "quit", Kind: "view", Title: "End the game", Subtitle: v.CurrentGame, Icon: "stop", Action: act})
	}
	if !who.Admin {
		return out
	}
	p := a.pairingOf(rig.ID)
	switch {
	case p.State == "waiting":
		out = append(out,
			sdui.Item{ID: "pin", Kind: "view", Title: "Type " + p.PIN + " into Sunshine", Subtitle: "On " + rig.Name + ", in Sunshine's PIN page. Then check again.", Icon: "key", Trailing: p.PIN},
			sdui.Item{ID: "pair-cancel", Kind: "view", Title: "Stop pairing", Icon: "close", Action: command(ui.CommandPairCancel, id)})
	case !v.Paired || v.State == "not-paired" || v.State == "cert-changed":
		sub := "Shows a PIN to type into Sunshine on that PC."
		if p.State == "failed" && p.Message != "" {
			sub = p.Message
		}
		out = append(out, sdui.Item{ID: "pair", Kind: "view", Title: "Pair", Subtitle: sub, Icon: "link", Action: command(ui.CommandPair, id)})
	default:
		out = append(out, sdui.Item{ID: "sync", Kind: "view", Title: "Read its games again", Subtitle: "What " + rig.Name + " offers now", Icon: "refresh", Action: command(ui.CommandSync, id)})
	}
	rm := command(ui.CommandRemove, id)
	rm.Confirm = "Remove " + rig.Name + ", its library and its games?"
	out = append(out, sdui.Item{ID: "remove", Kind: "view", Title: "Remove", Subtitle: "Forgets this PC, its library and its games", Icon: "delete", Action: rm})
	return out
}

// gameRows are a rig's games this person may play, each started on the PC by its row.
func (a *App) gameRows(ctx context.Context, who service.Identity, rig *rigRow) ([]sdui.Item, error) {
	items, err := a.Host.Items(ctx, host.On{User: who.User, Profile: who.Profile}, host.Query{Library: rig.LibraryID, Limit: 500})
	if err != nil {
		return nil, err
	}
	out := make([]sdui.Item, 0, len(items))
	for _, it := range items {
		id := strconv.FormatInt(it.ID, 10)
		out = append(out, sdui.Item{ID: id, Kind: "game", Title: it.Title, Subtitle: "Starts it on " + rig.Name + "; then Resume in Moonlight.", Icon: "game",
			Action: command(ui.CommandStart, id)})
	}
	return out, nil
}

// appRows are everything a rig offers, each a switch: in its library or not.
func (a *App) appRows(ctx context.Context, rig *rigRow) ([]sdui.Item, error) {
	apps, err := a.rigAppList(ctx, rig.ID)
	if err != nil {
		return nil, err
	}
	id := strconv.FormatInt(rig.ID, 10)
	out := make([]sdui.Item, 0, len(apps))
	for _, x := range apps {
		op := ui.CommandAppShow
		if x.Shown {
			op = ui.CommandAppHide
		}
		act := command(op, id)
		act.Params = map[string]any{"app": strconv.Itoa(x.ID)}
		out = append(out, sdui.Item{ID: strconv.Itoa(x.ID), Kind: "view", Title: x.Title, Control: &sdui.Control{Type: core.ControlSwitch, Value: x.Shown}, Action: act})
	}
	return out, nil
}

// localRows are this server's own Sunshine: its state and the switch that offers it.
func (a *App) localRows(ctx context.Context, who service.Identity) []sdui.Item {
	st := a.localState(ctx, who)
	op := ui.CommandLocalOn
	if st.Enabled {
		op = ui.CommandLocalOff
	}
	out := []sdui.Item{{ID: "enabled", Kind: "view", Title: "Offer this server's games", Subtitle: "Its Sunshine's games become a library, played like any game PC's.",
		Control: &sdui.Control{Type: core.ControlSwitch, Value: st.Enabled}, Action: &sdui.Action{Type: core.ActCommand, Op: op}}}
	installed := "Not found on this server. Install Sunshine (" + st.License + ") from " + st.Source + "."
	if st.Installed != "" {
		installed = "Installed: " + st.Installed
	}
	out = append(out, sdui.Item{ID: "installed", Kind: "view", Title: "Sunshine", Subtitle: installed, Icon: "info"})
	if st.Enabled {
		running := "Not running. Start Sunshine on this server."
		if st.Running {
			running = "Running."
		}
		out = append(out, sdui.Item{ID: "running", Kind: "view", Title: "Sunshine on this server", Subtitle: running, Icon: "info"})
		if st.Rig != nil {
			out = append(out, sdui.Item{ID: "rig", Kind: "view", Title: st.Rig.Name, Subtitle: rigLine(*st.Rig), Trailing: stateLabel(st.Rig.State), Icon: "devices",
				Route: "/page/" + ui.PageRig + "?item=" + strconv.FormatInt(st.Rig.ID, 10) + "&name=" + url.QueryEscape(st.Rig.Name)})
		}
	}
	return out
}
