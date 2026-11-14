// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wittywilla/wittymodule"
	"github.com/wittywilla/wittyrigs"
	"github.com/wittywilla/wittyrigs/ui"
)

func integration(t *testing.T) *wittymodule.Integration {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", wittymodule.IntegrationFile))
	if err != nil {
		t.Fatal(err)
	}
	in, err := wittymodule.ParseIntegration(b)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// integration.json passes the checks a server makes, against module.json.
func TestIntegrationFile(t *testing.T) {
	man, err := wittymodule.ParseManifest(wittyrigs.ManifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	in := integration(t)
	for _, p := range in.Check(man) {
		t.Errorf("%v", p)
	}
}

// Every route integration.json lists is one the service answers, and the other way round.
func TestRoutesAreDeclared(t *testing.T) {
	in := integration(t)
	mux := http.NewServeMux()
	Routes(mux)
	declared := map[string]bool{}
	for _, r := range in.HTTP {
		for _, m := range r.Methods {
			declared[m+" "+r.Path] = true
			req := httptest.NewRequest(m, strings.NewReplacer("{id}", "1", "{app}", "2", "{sid}", "x").Replace(r.Path), nil)
			if _, pattern := mux.Handler(req); pattern == "" {
				t.Errorf("%s %s isn't answered", m, r.Path)
			}
		}
	}
	// Each command runs one of them.
	for _, c := range ui.Commands() {
		p := strings.TrimPrefix(c.Path, "/api/modules/"+ui.Module)
		p = strings.NewReplacer("{item}", "{id}").Replace(p)
		if !declared[c.Method+" "+p] {
			t.Errorf("%s: no route %s %s", c.Op, c.Method, p)
		}
	}
}
