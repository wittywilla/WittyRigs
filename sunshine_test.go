// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittyrigs

import (
	"errors"
	"testing"

	"github.com/wittywilla/wittymodule"
)

func noEnv(string) string { return "" }

func notOnPath(string) (string, error) { return "", errors.New("no PATH") }

// The server gives this helper no PATH or ProgramFiles, and Sunshine is still found where its
// installers put it.
func TestFindWithNoEnvironment(t *testing.T) {
	for goos, want := range map[string]string{
		"windows": `C:\Program Files\Sunshine\sunshine.exe`,
		"linux":   "/usr/bin/sunshine",
		"freebsd": "/usr/local/bin/sunshine",
		"darwin":  "/opt/homebrew/bin/sunshine",
	} {
		got := find(goos, noEnv, func(p string) bool { return p == want }, notOnPath)
		if got != want {
			t.Errorf("%s: found %q, want %q", goos, got, want)
		}
	}
}

func TestFindOnWindowsUsesTheSystemDrive(t *testing.T) {
	want := `D:\Program Files\Sunshine\sunshine.exe`
	env := func(k string) string {
		if k == "SystemRoot" {
			return `D:\Windows`
		}
		return ""
	}
	if got := find("windows", env, func(p string) bool { return p == want }, notOnPath); got != want {
		t.Errorf("found %q, want %q", got, want)
	}
}

func TestFindFlatpakForThisAccount(t *testing.T) {
	want := "/home/games/.local/share/flatpak/exports/bin/dev.lizardbyte.app.Sunshine"
	env := func(k string) string {
		if k == "HOME" {
			return "/home/games"
		}
		return ""
	}
	if got := find("linux", env, func(p string) bool { return p == want }, notOnPath); got != want {
		t.Errorf("found %q, want %q", got, want)
	}
}

func TestFindFallsBackToPathThenNothing(t *testing.T) {
	none := func(string) bool { return false }
	onPath := func(string) (string, error) { return "/somewhere/sunshine", nil }
	if got := find("linux", noEnv, none, onPath); got != "/somewhere/sunshine" {
		t.Errorf("found %q", got)
	}
	if got := find("linux", noEnv, none, notOnPath); got != "" {
		t.Errorf("found %q where there is nothing", got)
	}
}

func TestManifestFitsThisServer(t *testing.T) {
	m, err := wittymodule.ParseManifest(ManifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, server := range []string{"0.8.0-alpha", "0.8.3"} {
		if err := m.Supports(server, wittymodule.MinAPI, wittymodule.API); err != nil {
			t.Errorf("%s: %v", server, err)
		}
	}
	// Module API 4 carries its own SDUI, which a server before 0.8.0-alpha can't read.
	if err := m.Supports("0.6.5", wittymodule.MinAPI, wittymodule.API); err == nil {
		t.Error("a 0.6 server would take this version")
	}
}
