// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

// Package wittyrigs is the advanced option of hosting demanding PC games on the server's own
// hardware, through Sunshine as a separate program that its owner installs. It finds that program.
// Everything that talks to Sunshine is in WittyCouch's gamestream package, as it's the same
// protocol as reaching any other PC. See docs/GAME-RIGS.md.
package wittyrigs

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const (
	// License is Sunshine's own, read from its repository.
	License = "GPL-3.0"
	// Source is its project.
	Source = "https://github.com/LizardByte/Sunshine"
)

// Find says where Sunshine is installed on this machine, if anywhere it usually goes.
//
// The server starts this helper with almost no environment (it asks for no permissions, so it
// gets no PATH or ProgramFiles), so the usual places are listed as they are, and the
// environment is only a hint on top of them.
func Find() string {
	return find(runtime.GOOS, os.Getenv, isFile, exec.LookPath)
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// places lists where Sunshine's own installers and packages put it, most likely first.
func places(goos string, getenv func(string) string) []string {
	var out []string
	switch goos {
	case "windows":
		var roots []string
		for _, k := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
			if v := getenv(k); v != "" {
				roots = append(roots, v)
			}
		}
		drive := "C:"
		if root := getenv("SystemRoot"); len(root) >= 2 && root[1] == ':' {
			drive = root[:2]
		}
		roots = append(roots, drive+`\Program Files`, drive+`\Program Files (x86)`)
		for _, r := range roots {
			out = append(out, strings.TrimRight(r, `\/`)+`\Sunshine\sunshine.exe`)
		}
	case "darwin":
		out = []string{"/Applications/Sunshine.app/Contents/MacOS/sunshine", "/opt/homebrew/bin/sunshine", "/usr/local/bin/sunshine"}
	default:
		out = []string{"/usr/bin/sunshine", "/usr/local/bin/sunshine", "/opt/sunshine/sunshine",
			// The Flatpak, installed for everyone or for this account.
			"/var/lib/flatpak/exports/bin/dev.lizardbyte.app.Sunshine"}
		if home := getenv("HOME"); home != "" {
			out = append(out, home+"/.local/share/flatpak/exports/bin/dev.lizardbyte.app.Sunshine")
		}
	}
	return out
}

func find(goos string, getenv func(string) string, exists func(string) bool, lookPath func(string) (string, error)) string {
	for _, p := range places(goos, getenv) {
		if exists(p) {
			return p
		}
	}
	if p, err := lookPath("sunshine"); err == nil {
		return p
	}
	return ""
}
