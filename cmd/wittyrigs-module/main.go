// SPDX-License-Identifier: AGPL-3.0-or-later
//
// WittyRigs - An advanced option: host demanding PC games on your server's own hardware.
// Copyright (C) 2026 WittyWilla and contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

// Command wittyrigs-module is WittyRigs' service (module API 5): the only way a server reaches it.
// It answers WittyRigs' own routes (package api), talks to the gaming PCs, and holds the streams
// of their games played in a page. It runs while it's in use, and listens on this machine only.
// See package service in WittyModule.
package main

import (
	"net/http"

	"github.com/wittywilla/wittymodule/sdui"
	"github.com/wittywilla/wittymodule/service"
	"github.com/wittywilla/wittyrigs"
	"github.com/wittywilla/wittyrigs/api"
)

func main() {
	service.Main(service.Config{
		Manifest: wittyrigs.ManifestJSON,
		Busy:     api.Busy,
		OnStop: func() {
			if a, err := api.Get(); err == nil {
				a.Stop()
			}
		},
		UI:        sdui.Answers{Items: api.SDUIItems},
		HTTP:      func(mux *http.ServeMux) { api.Routes(mux); api.Watch() },
		Lifecycle: api.Lifecycle,
	})
}
