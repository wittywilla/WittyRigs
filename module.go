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

package wittyrigs

import _ "embed"

// ManifestJSON is this module's manifest (docs/MODULES.md, "The manifest").
//
//go:embed module.json
var ManifestJSON []byte
