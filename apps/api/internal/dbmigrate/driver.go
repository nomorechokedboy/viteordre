// Package dbmigrate wraps golang-migrate for Turso databases.
//
// driver.go is adapted from github.com/golang-migrate/migrate/v4/database/sqlite, which is
// part of golang-migrate (MIT License, original work Copyright (c) 2016 Matthias Kadenbach,
// see the LICENSE file in that repository). Changes from upstream:
//   - it does not import modernc.org/sqlite, which would link a second SQLite engine into
//     the binary
//   - Open by URL and Drop are not supported, Drop because Turso treats VACUUM as
//     experimental and because dropping a database is deliberately not exposed
//   - Version no longer swallows real database errors
//   - SetVersion rolls back when its DELETE fails
//
// The version table has the same layout as upstream, so switching drivers later is safe.
package dbmigrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync/atomic"

	"github.com/golang-migrate/migrate/v4/database"
)

// DefaultTable is the version table golang-migrate uses by default.
const DefaultTable = "schema_migrations"

// ErrUnsupported is returned by the operations this driver deliberately lacks.
var ErrUnsupported = errors.New("dbmigrate: operation not supported by the Turso driver")

var tableNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Driver implements golang-migrate's database.Driver on top of a *sql.DB opened with the
// Turso driver. Statements inside one migration file run through tursogo's multi-statement
// Exec, one statement at a time.
type Driver struct {
	db     *sql.DB
	table  string
	locked atomic.Bool
}

var _ database.Driver = (*Driver)(nil)

// NewDriver creates the version table if needed and returns a Driver. The Driver does not
// own db until Close is called, and Close closes it.
func NewDriver(db *sql.DB, table string) (*Driver, error) {
	if table == "" {
		table = DefaultTable
	}
	if !tableNameRe.MatchString(table) {
		return nil, fmt.Errorf("dbmigrate: invalid migrations table name %q", table)
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	d := &Driver{db: db, table: table}
	if err := d.ensureVersionTable(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Driver) ensureVersionTable() error {
	// Two separate statements, so this also works with drivers that reject multi-statement Exec.
	stmts := []string{
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (version uint64,dirty bool)", d.table),
		fmt.Sprintf("CREATE UNIQUE INDEX IF NOT EXISTS version_unique ON %s (version)", d.table),
	}
	for _, s := range stmts {
		if _, err := d.db.Exec(s); err != nil {
			return &database.Error{OrigErr: err, Query: []byte(s)}
		}
	}
	return nil
}

// Open is not supported, use NewDriver.
func (d *Driver) Open(string) (database.Driver, error) { return nil, ErrUnsupported }

// Close closes the underlying *sql.DB.
func (d *Driver) Close() error { return d.db.Close() }

// Drop is not supported on purpose.
func (d *Driver) Drop() error { return ErrUnsupported }

// Lock is an in-process guard. A Turso file has one owner process, so no database-level
// advisory lock is needed.
func (d *Driver) Lock() error {
	if !d.locked.CompareAndSwap(false, true) {
		return database.ErrLocked
	}
	return nil
}

// Unlock releases the in-process guard.
func (d *Driver) Unlock() error {
	if !d.locked.CompareAndSwap(true, false) {
		return database.ErrNotLocked
	}
	return nil
}

// Run executes one migration file inside a transaction. Turso supports transactional DDL, a
// failed file leaves no partial schema behind.
func (d *Driver) Run(migration io.Reader) error {
	body, err := io.ReadAll(migration)
	if err != nil {
		return err
	}
	query := string(body)

	tx, err := d.db.BeginTx(context.Background(), nil)
	if err != nil {
		return &database.Error{OrigErr: err, Err: "transaction start failed"}
	}
	if _, err := tx.Exec(query); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			err = errors.Join(err, rbErr)
		}
		return &database.Error{OrigErr: err, Query: body}
	}
	if err := tx.Commit(); err != nil {
		return &database.Error{OrigErr: err, Err: "transaction commit failed"}
	}
	return nil
}

// SetVersion records the current version and dirty flag, replacing the single version row.
func (d *Driver) SetVersion(version int, dirty bool) error {
	tx, err := d.db.BeginTx(context.Background(), nil)
	if err != nil {
		return &database.Error{OrigErr: err, Err: "transaction start failed"}
	}

	del := "DELETE FROM " + d.table
	if _, err := tx.Exec(del); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			err = errors.Join(err, rbErr)
		}
		return &database.Error{OrigErr: err, Query: []byte(del)}
	}

	// A dirty NilVersion is still written, so a failed first down migration is not lost
	// (golang-migrate issue 330).
	if version >= 0 || (version == database.NilVersion && dirty) {
		ins := fmt.Sprintf("INSERT INTO %s (version, dirty) VALUES (?, ?)", d.table)
		if _, err := tx.Exec(ins, version, dirty); err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				err = errors.Join(err, rbErr)
			}
			return &database.Error{OrigErr: err, Query: []byte(ins)}
		}
	}

	if err := tx.Commit(); err != nil {
		return &database.Error{OrigErr: err, Err: "transaction commit failed"}
	}
	return nil
}

// Version returns the current version and dirty flag, or database.NilVersion when no
// migration has been applied.
func (d *Driver) Version() (version int, dirty bool, err error) {
	q := "SELECT version, dirty FROM " + d.table + " LIMIT 1"
	var v int64
	if err := d.db.QueryRow(q).Scan(&v, &dirty); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return database.NilVersion, false, nil
		}
		return 0, false, &database.Error{OrigErr: err, Query: []byte(q)}
	}
	return int(v), dirty, nil
}
