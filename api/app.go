// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

// Package api is WittyRigs' own HTTP API (module API 5): the routes WittyMediaServer passes to it at
// /api/modules/wittyrigs/... once it has checked who may call them. It keeps its data in its own
// database, and asks the server through the host API for what only the server has: the games
// libraries that hold each rig's games, who may see them, and the Moonlight host program.
//
// Game rigs are gaming PCs running Sunshine, each a source of games. A PC reached this way may
// hold everything its owner has, so:
//
//   - Adding, pairing and managing a rig is for admins. Nobody else can name an address.
//   - Everyone else only ever reaches addresses an admin registered, on fixed paths.
//   - This server's key stays in a file, and nothing secret goes to a browser.
//   - What someone may play is decided by the server's rules for every other item, and checked
//     again at the moment a stream starts.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/wittywilla/wittycouch/gamestream"
	"github.com/wittywilla/wittymodule/host"
)

// DataEnv is where the server says WittyRigs' own folder is.
const DataEnv = "WITTYMODULE_DATA"

// App is what the routes need: the database and folder, the way back to the server, and what's
// held in memory.
type App struct {
	DB   *DB
	Dir  string
	Host *host.Client
	Log  *slog.Logger
	// Prepare is added to how the Moonlight host program starts (tests stand in for it).
	Prepare func(*exec.Cmd)

	mu       sync.Mutex
	identity *gamestream.Identity
	statuses map[int64]rigCached
	pairs    map[int64]*rigPairing
	lastSync map[int64]time.Time
	lastWake map[int64]time.Time
	starter  map[int64]rigStarter // rig: who last started something on it through this server
	syncing  map[int64]*sync.Mutex
	finding  sync.Mutex
	streams  map[string]*stream
	starting map[int64]bool // who has a stream on its way, as that takes a minute
	stopped  bool
}

// New makes an App over a database, a folder and a host client.
func New(db *DB, dir string, h *host.Client) *App {
	return &App{DB: db, Dir: dir, Host: h, Log: slog.Default(), statuses: map[int64]rigCached{}, pairs: map[int64]*rigPairing{},
		lastSync: map[int64]time.Time{}, lastWake: map[int64]time.Time{}, starter: map[int64]rigStarter{}, syncing: map[int64]*sync.Mutex{},
		streams: map[string]*stream{}, starting: map[int64]bool{}}
}

var (
	appMu  sync.Mutex
	shared *App
)

// Get opens the database and reaches the server the first time something needs them.
func Get() (*App, error) {
	appMu.Lock()
	defer appMu.Unlock()
	if shared != nil {
		return shared, nil
	}
	dir := os.Getenv(DataEnv)
	db, err := OpenDB(dir)
	if err != nil {
		return nil, err
	}
	h, err := host.New()
	if err != nil {
		db.Close()
		return nil, err
	}
	shared = New(db, dir, h)
	return shared, nil
}

// Use makes a the App the routes use (tests).
func Use(a *App) {
	appMu.Lock()
	shared = a
	appMu.Unlock()
}

func (a *App) ident() (*gamestream.Identity, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.identity != nil {
		return a.identity, nil
	}
	id, err := gamestream.LoadOrCreateIdentity(filepath.Join(a.Dir, "identity"))
	if err != nil {
		return nil, fmt.Errorf("the rig identity can't be read or made: %w", err)
	}
	a.identity = id
	return id, nil
}

// ---------------------------------------------------------------- the host API

// Library is a games library the server keeps for a rig.
type Library struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Console string `json:"console"`
}

func (a *App) createLibrary(ctx context.Context, name string) (Library, error) {
	var l Library
	return l, a.Host.Do(ctx, host.On{}, "POST", "/libraries", map[string]string{"name": name}, &l)
}

func (a *App) renameLibrary(ctx context.Context, id int64, name string) error {
	return a.Host.Do(ctx, host.On{}, "PATCH", "/libraries/"+strconv.FormatInt(id, 10), map[string]string{"name": name}, nil)
}

func (a *App) deleteLibrary(ctx context.Context, id int64) error {
	err := a.Host.Do(ctx, host.On{}, "DELETE", "/libraries/"+strconv.FormatInt(id, 10), nil, nil)
	if host.IsNotFound(err) {
		return nil // gone already
	}
	return err
}

// saveGame adds a game to a rig's library, or updates it. The title is always the PC's, as that's
// the name Moonlight needs.
func (a *App) saveGame(ctx context.Context, library int64, path, title, tagline string) (int64, bool, error) {
	var out struct {
		ID  int64 `json:"id"`
		New bool  `json:"new"`
	}
	err := a.Host.Do(ctx, host.On{}, "PUT", "/libraries/"+strconv.FormatInt(library, 10)+"/items",
		map[string]string{"path": path, "title": title, "tagline": tagline}, &out)
	return out.ID, out.New, err
}

func (a *App) removeGame(ctx context.Context, item int64) {
	_ = a.Host.Do(ctx, host.On{}, "DELETE", "/items/"+strconv.FormatInt(item, 10), nil, nil)
}

// setPoster gives a game the PC's picture; with none, the PC's picture goes (one chosen by hand
// stays, as the server decides).
func (a *App) setPoster(ctx context.Context, item int64, picture []byte) error {
	p := "/items/" + strconv.FormatInt(item, 10) + "/poster"
	if picture == nil {
		return a.Host.Do(ctx, host.On{}, "DELETE", p, nil, nil)
	}
	return a.Host.Do(ctx, host.On{}, "PUT", p, bytesReader(picture), nil)
}

// program is the Moonlight host program (the wittycouchhost module), as the server runs it: path,
// then args before the program's own.
func (a *App) program(ctx context.Context) (string, []string, error) {
	var out struct {
		Path string   `json:"path"`
		Args []string `json:"args"`
	}
	if p := os.Getenv("WMS_WITTYCOUCH_HOST"); p != "" {
		return p, nil, nil
	}
	if err := a.Host.Do(ctx, host.On{}, "GET", "/programs/wittycouchhost", nil, &out); err != nil {
		return "", nil, err
	}
	if out.Path == "" {
		return "", nil, errors.New("no program")
	}
	return out.Path, out.Args, nil
}

// prepareFor is how the program starts: apart from this service's files, with whatever the server
// runs it inside (a sandbox's launcher) before it.
func (a *App) prepareFor(path string, args []string) func(*exec.Cmd) {
	return func(c *exec.Cmd) {
		c.Dir = os.TempDir()
		if len(args) > 0 {
			c.Args = append(append([]string{path}, args...), c.Args[1:]...)
		}
		if a.Prepare != nil {
			a.Prepare(c)
		}
	}
}
