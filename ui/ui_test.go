// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package ui

import (
	"slices"
	"testing"

	"github.com/wittywilla/wittymodule/sdui"
	core "github.com/wittywilla/wittymodule/sdui/core"
	"github.com/wittywilla/wittymodule/sdui/sduitest"
)

// sdui.json is this definition, and it passes the kit's checks and the server's own. Run with
// -update-sdui to write it again.
func TestSDUIFile(t *testing.T) {
	sduitest.Golden(t, UI(), "../sdui.json")
	b, err := sdui.JSON(UI())
	if err != nil {
		t.Fatal(err)
	}
	m, err := core.ParseModuleUI(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Commands) != len(Commands()) {
		t.Fatalf("%d commands read back, of %d", len(m.Commands), len(Commands()))
	}
	for _, is := range core.ValidateModuleUI(m, Module, core.ModuleChecks{ColorToken: func(s string) bool { return slices.Contains(sdui.ColorTokens, s) }}) {
		if is.Severity == "error" {
			t.Errorf("%s: %s", is.Path, is.Message)
		}
	}
}

// Every page a person needs is here for the TV apps, which have no web view.
func TestTVPages(t *testing.T) {
	have := map[string]bool{}
	for _, p := range UI().Pages {
		have[p.ID] = true
	}
	for _, id := range []string{PageRigs, PageRig, PageLocal} {
		if !have[id] {
			t.Errorf("no page %s", id)
		}
	}
}
