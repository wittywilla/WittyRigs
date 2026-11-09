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

// Command wittyrigs-module is WittyRigs as a helper process: the only way a server reaches it. It
// only looks for Sunshine on this machine. See WittyModule for the protocol.
package main

import (
	"encoding/json"
	"fmt"

	"github.com/wittywilla/wittymodule"
	"github.com/wittywilla/wittyrigs"
)

func main() {
	wittymodule.Main(wittyrigs.ManifestJSON, func(method string, _ json.RawMessage) (any, []byte, error) {
		if method == "find" {
			return struct {
				Path    string `json:"path"`
				License string `json:"license"`
				Source  string `json:"source"`
			}{wittyrigs.Find(), wittyrigs.License, wittyrigs.Source}, nil, nil
		}
		return nil, nil, &wittymodule.CallError{Code: "unknown-method", Message: fmt.Sprintf("this version has no %q", method)}
	})
}
