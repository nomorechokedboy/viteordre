package dbmigrate

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// Kind selects one of the two migration sets.
type Kind string

const (
	// Control is the control-plane database: merchants, members, invitations, webhook routing.
	Control Kind = "control"
	// Tenant is the per-merchant database.
	Tenant Kind = "tenant"
)

// ParseKind validates a target name.
func ParseKind(s string) (Kind, error) {
	switch Kind(s) {
	case Control, Tenant:
		return Kind(s), nil
	}
	return "", fmt.Errorf("unknown target %q, expected control or tenant", s)
}

// New returns a golang-migrate instance for kind reading files from fsys, whose root holds
// one directory per kind, and applying them to db. Closing the returned Migrate closes db.
func New(fsys fs.FS, kind Kind, db *sql.DB) (*migrate.Migrate, error) {
	src, err := iofs.New(fsys, string(kind))
	if err != nil {
		return nil, fmt.Errorf("dbmigrate: open %s migrations: %w", kind, err)
	}
	drv, err := NewDriver(db, DefaultTable)
	if err != nil {
		_ = src.Close()
		return nil, fmt.Errorf("dbmigrate: prepare %s database: %w", kind, err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "turso", drv)
	if err != nil {
		_ = src.Close()
		return nil, err
	}
	return m, nil
}

// Latest returns the highest migration version available for kind.
func Latest(fsys fs.FS, kind Kind) (uint, error) {
	src, err := iofs.New(fsys, string(kind))
	if err != nil {
		return 0, fmt.Errorf("dbmigrate: open %s migrations: %w", kind, err)
	}
	defer src.Close()

	v, err := src.First()
	if err != nil {
		return 0, fmt.Errorf("dbmigrate: no %s migrations found: %w", kind, err)
	}
	for {
		next, err := src.Next(v)
		if errors.Is(err, fs.ErrNotExist) {
			return v, nil
		}
		if err != nil {
			return 0, err
		}
		v = next
	}
}
