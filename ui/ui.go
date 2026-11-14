// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

// Package ui is WittyRigs' server-driven UI (WittyMediaServer docs/SDUI-MODULES.md): its screens on
// every app, the Apple TV and Android TV apps included, which have no web view for its web part.
// sdui.json at the repository's root is written from UI and held equal to it by a test.
//
// Everything on these pages works with a remote: rows open pages or run WittyRigs' own commands
// (its routes, which check again who may), and nothing needs typing. Adding a PC by its address,
// renaming one and typing a PIN for another device stay on a computer, in the web part.
package ui

import (
	"github.com/wittywilla/wittymodule/sdui"
	core "github.com/wittywilla/wittymodule/sdui/core"
)

// Module is this module's ID.
const Module = "wittyrigs"

// Sources, answered by the service (package api, sdui.go).
const (
	SourceRigs  = Module + ".rigs"
	SourceRig   = Module + ".rig"
	SourceFound = Module + ".found"
	SourceLocal = Module + ".local"
)

// Settings (an administrator's, for the whole server).
const (
	SettingMaxStreams = "maxStreams"
	SettingUDPPort    = "udpPort"
	SettingPublicIP   = "publicIp"
	SettingSTUN       = "stun"
)

// Pages.
const (
	PageRigs  = Module
	PageRig   = Module + "-rig"
	PageLocal = Module + "-local"
)

// Commands: each runs one of WittyRigs' own routes.
const (
	CommandWake       = Module + ".rig.wake"
	CommandQuit       = Module + ".rig.quit"
	CommandPair       = Module + ".rig.pair"
	CommandPairCancel = Module + ".rig.pairCancel"
	CommandSync       = Module + ".rig.sync"
	CommandRemove     = Module + ".rig.remove"
	CommandAdd        = Module + ".rig.add"
	CommandAppShow    = Module + ".app.show"
	CommandAppHide    = Module + ".app.hide"
	CommandStart      = Module + ".game.start"
	CommandLocalOn    = Module + ".local.on"
	CommandLocalOff   = Module + ".local.off"
)

// Commands are WittyRigs' own commands, as sdui.json declares them.
func Commands() []sdui.Command {
	routes := "/api/modules/" + Module
	return []sdui.Command{
		{Op: CommandWake, Label: "Wake it", Method: "POST", Path: routes + "/rigs/{item}/wake",
			Description: "Sends a game PC (item: its ID) the wake-up signal, for anyone who can see it."},
		{Op: CommandQuit, Label: "End the game", Method: "POST", Path: routes + "/rigs/{item}/quit",
			Description: "Ends what's running on a game PC (item: its ID): an administrator, or whoever started it from here."},
		{Op: CommandPair, Label: "Pair", Method: "POST", Path: routes + "/rigs/{item}/pair",
			Description: "Administrators: starts pairing with a game PC (item: its ID); the PIN to type into Sunshine on that PC is shown on the PC's page."},
		{Op: CommandPairCancel, Label: "Stop pairing", Method: "DELETE", Path: routes + "/rigs/{item}/pair",
			Description: "Administrators: stops waiting for a PIN from a game PC (item: its ID)."},
		{Op: CommandSync, Label: "Read its games again", Method: "POST", Path: routes + "/rigs/{item}/sync",
			Description: "Administrators: reads a game PC's games again (item: its ID)."},
		{Op: CommandRemove, Label: "Remove", Method: "DELETE", Path: routes + "/rigs/{item}",
			Description: "Administrators: forgets a game PC (item: its ID), its library and its games."},
		{Op: CommandAdd, Label: "Add", Method: "POST", Path: routes + "/rigs", Body: `{"address":"{item}"}`,
			Description: "Administrators: adds a PC found on the network (item: its address and port), which has to answer as Sunshine."},
		{Op: CommandAppShow, Label: "Show it", Method: "PUT", Path: routes + "/rigs/{item}/apps/{app}", Body: `{"shown":true}`,
			Description: "Administrators: puts one of a game PC's apps (item: the PC's ID; params.app: the app's) in its library.",
			Params:      []core.CommandParam{{Name: "app", Type: "string", Label: "App", Required: true}}},
		{Op: CommandAppHide, Label: "Hide it", Method: "PUT", Path: routes + "/rigs/{item}/apps/{app}", Body: `{"shown":false}`,
			Description: "Administrators: takes one of a game PC's apps (item: the PC's ID; params.app: the app's) out of its library.",
			Params:      []core.CommandParam{{Name: "app", Type: "string", Label: "App", Required: true}}},
		{Op: CommandStart, Label: "Start it on the PC", Method: "POST", Path: routes + "/games/{item}/start",
			Description: "Starts a game PC's game (item: the game's ID) on the PC, to pick up in Moonlight with Resume."},
		{Op: CommandLocalOn, Label: "Turn on", Method: "PUT", Path: routes + "/local", Body: `{"enabled":true}`,
			Description: "Administrators: offers the games of this server's own Sunshine as a library."},
		{Op: CommandLocalOff, Label: "Turn off", Method: "PUT", Path: routes + "/local", Body: `{"enabled":false}`,
			Description: "Administrators: stops offering this server's own Sunshine, and takes its library away."},
	}
}

// UI is the module's SDUI.
func UI() *sdui.Module {
	m := sdui.New(Module, "Game rigs").Colours(map[string]string{"accent": "#7F46B8"})
	m.Commands = Commands()
	m.AddSource(sdui.SourceSpec{Type: SourceRigs, Label: "Game rigs", Icon: "devices", Title: "Game PCs",
		Description: "The gaming PCs running Sunshine this profile can see (those with a game it may play), each with how it's doing, opening its own page.",
		Params:      []sdui.PropSpec{{Name: "limit", Type: "int", Label: "At most", Default: 50, Min: sdui.Int(1), Max: sdui.Int(100)}},
		Yields:      []string{"view"}, CardStyle: "tile"})
	m.AddSource(sdui.SourceSpec{Type: SourceRig, Label: "A game PC", Icon: "devices", Title: "",
		Description: "One game PC ($item): with part actions, what can be done to it now (wake it, end what's running, and for an administrator pairing, with the PIN to type into Sunshine); games, its games this profile may play, each starting it on the PC; apps, for an administrator, everything it offers, each shown in its library or not.",
		Params: []sdui.PropSpec{
			{Name: "rig", Type: "ref", Label: "Game PC", Required: true, Help: "$item on its page."},
			{Name: "part", Type: "enum", Label: "Which rows", Default: "actions",
				Options: []sdui.EnumOption{{Value: "actions", Label: "What can be done"}, {Value: "games", Label: "Its games"}, {Value: "apps", Label: "Its apps (administrators)"}}},
		},
		Yields: []string{"view", "game"}, CardStyle: "tile"})
	m.AddSource(sdui.SourceSpec{Type: SourceFound, Label: "PCs on the network", Icon: "search", Title: "On your network",
		Description: "For an administrator: the PCs on this server's network that answer as Sunshine, each to add with one press. Nothing for anyone else.",
		Yields:      []string{"view"}, CardStyle: "tile"})
	m.AddSource(sdui.SourceSpec{Type: SourceLocal, Label: "This server's Sunshine", Icon: "settings", Title: "This server's Sunshine",
		Description: "For an administrator: whether Sunshine is installed and running on this server, and a row to offer its games or stop. Nothing for anyone else.",
		Yields:      []string{"view"}, CardStyle: "tile"})

	admin, notAdmin := true, false
	forAdmins, forOthers := &sdui.Condition{Admin: &admin}, &sdui.Condition{Admin: &notAdmin}
	when := func(s sdui.Section, c *sdui.Condition) sdui.Section { s.When = c; return s }
	list := func(id, title string, src sdui.Source, kv ...any) sdui.Section {
		return sdui.Sec(id, "List", sdui.Props(append([]any{"title", title, "focusable", true}, kv...)...), src)
	}
	item := func(id, title, subtitle, icon string, kv ...any) sdui.Section {
		return sdui.Sec(id, "ListItem", sdui.Props(append([]any{"title", title, "subtitle", subtitle, "icon", icon}, kv...)...), nil)
	}
	again := sdui.Sec("again", "List", sdui.Props("title", ""), nil,
		item("check", "Check again", "Reads again how each PC is doing.", "history", "op", "refresh"))

	rigs := sdui.Page{ID: PageRigs, Kind: "custom", Title: "Game PCs", Icon: "devices", Scope: Module,
		Description: "Game rigs on every app: the gaming PCs this profile can see, and for an administrator the PCs on the network to add and this server's own Sunshine.",
		Sections: []sdui.Section{
			list("rigs", "Game PCs", sdui.Src(SourceRigs), "empty", "message", "emptyText", "No game PCs here yet. An administrator adds a gaming PC running Sunshine; its games become a library."),
			when(list("found", "On your network", sdui.Src(SourceFound), "empty", "message", "emptyText", "No other PC on this network answers as Sunshine. Start Sunshine on the PC, or add it by its address in Settings, Game rigs, in a browser."), forAdmins),
			when(sdui.Sec("more", "List", sdui.Props("title", "This server"), nil,
				item("local", "This server's own Sunshine", "Host demanding games on this server's own hardware", "settings", "to", "/page/"+PageLocal)), forAdmins),
			again,
		}}
	m.AddPage(rigs)

	rig := sdui.Page{ID: PageRig, Kind: "custom", Title: "{title}", Icon: "devices", Scope: Module,
		Description: "One game PC on every app: wake it, end what's running, start one of its games to pick up in Moonlight, and for an administrator pair it and choose which of its apps are games. Opened with ?item=<its ID>&name=<its name>.",
		Sections: []sdui.Section{
			list("actions", "", sdui.Src(SourceRig, "rig", "$item", "part", "actions")),
			list("games", "Games", sdui.Src(SourceRig, "rig", "$item", "part", "games"), "empty", "message", "emptyText", "No games of this PC's for you here."),
			when(list("apps", "In its library", sdui.Src(SourceRig, "rig", "$item", "part", "apps"), "empty", "message", "emptyText", "This PC hasn't said what it offers yet. Pair it first."), forAdmins),
			when(sdui.Notice("others", "info", "Starting a game here runs it on the PC; then open Moonlight, choose the PC and the game, and Resume."), forOthers),
			again,
		}}
	m.AddPage(rig)

	local := sdui.Page{ID: PageLocal, Kind: "custom", Title: "This server's Sunshine", Icon: "settings", Scope: Module,
		Description: "For administrators, on every app: whether this server's own Sunshine is offered as a library, and its state.",
		Sections: []sdui.Section{
			when(sdui.Notice("admins", "info", "Only an administrator sets this up."), forOthers),
			when(list("local", "", sdui.Src(SourceLocal)), forAdmins),
			when(sdui.Notice("about", "info", "An advanced option: for a server with a good graphics card and PC games of its own. Sunshine is installed separately; WittyRigs only looks on this machine."), forAdmins),
			again,
		}}
	m.AddPage(local)

	// Playing a PC's game in a page: the stream passes through this server.
	m.AddSettings(sdui.SettingsPage{ID: Module, Title: "Game rigs", Icon: "devices", Groups: []sdui.SettingsGroup{
		{Title: "Playing in a page", Footer: "A stream goes from the PC through this server to the browser, at up to its full bitrate. The server doesn't re-encode it.", Rows: []sdui.SettingRow{
			{Key: SettingMaxStreams, Kind: "int", Label: "Streams at once", Default: 2, Min: sdui.Int(1), Max: sdui.Int(16)},
		}},
		{Title: "Reaching browsers away from home", Footer: "Nothing needed at home. Never forward Sunshine's own ports, and never port 47990, its web interface.", Rows: []sdui.SettingRow{
			{Key: SettingUDPPort, Kind: "int", Label: "UDP port", Help: "One port for every stream, to forward on a router or publish from a container. 0: any free port.", Default: 0, Min: sdui.Int(0), Max: sdui.Int(65535)},
			{Key: SettingPublicIP, Kind: "string", Label: "This server's public address", MaxLength: 100},
			{Key: SettingSTUN, Kind: "string", Label: "STUN server", Help: "None: nothing leaves your network.", MaxLength: 200},
		}},
	}})
	return m
}
