// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	_ "modernc.org/sqlite"
)

// DBFile is WittyRigs' database, in its own folder (WITTYMODULE_DATA). It starts empty: what a
// server kept of rigs before module API 5 isn't read (no backwards compatibility).
const DBFile = "wittyrigs.db"

// migrations are the schema, one step each, applied once and in order.
var migrations = []string{
	// 1: rigs, each holding a games library of the server's; the apps each offers, the item of the
	// library each shown one is and which picture it was last given (a hash, so an unchanged
	// picture isn't sent again); a few settings of the module's own. The certificate is the
	// PC's own, which isn't secret: this server's key is in a file (identity/).
	`CREATE TABLE rigs (
	   id INTEGER PRIMARY KEY,
	   library_id INTEGER NOT NULL UNIQUE,
	   name TEXT NOT NULL, address TEXT NOT NULL, port INTEGER NOT NULL DEFAULT 0,
	   remote_address TEXT NOT NULL DEFAULT '', allow_public INTEGER NOT NULL DEFAULT 0,
	   uuid TEXT NOT NULL DEFAULT '', mac TEXT NOT NULL DEFAULT '', cert TEXT NOT NULL DEFAULT '',
	   paired_at INTEGER, created_at INTEGER NOT NULL);
	 CREATE TABLE apps (
	   rig_id INTEGER NOT NULL REFERENCES rigs(id) ON DELETE CASCADE,
	   app_id INTEGER NOT NULL, title TEXT NOT NULL, shown INTEGER NOT NULL,
	   item_id INTEGER, art TEXT NOT NULL DEFAULT '',
	   PRIMARY KEY(rig_id, app_id));
	 CREATE INDEX apps_item ON apps(item_id);
	 CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
}

// DB is WittyRigs' database.
type DB struct{ *sql.DB }

// OpenDB opens (or makes) the database in dir and brings its schema up to date.
func OpenDB(dir string) (*DB, error) {
	if dir == "" {
		return nil, errors.New("wittyrigs: no folder of its own (the data permission)")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, DBFile)+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	d := &DB{db}
	if err := d.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return err
	}
	var have int
	_ = d.QueryRowContext(ctx, `SELECT CAST(value AS INTEGER) FROM meta WHERE key = 'version'`).Scan(&have)
	for i := have; i < len(migrations); i++ {
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("wittyrigs: migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('version', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, strconv.Itoa(i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Setting reads one of the module's own settings.
func (d *DB) Setting(ctx context.Context, key, def string) string {
	var v string
	if err := d.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		return def
	}
	return v
}

// SetSetting writes one.
func (d *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := d.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
