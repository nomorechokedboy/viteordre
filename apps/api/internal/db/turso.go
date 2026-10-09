// Package db opens Turso database files and wraps them for dbx.
//
// It deliberately does not import internal/config: that package loads configuration and
// configures logging in an init function, which a CLI should not trigger.
package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	turso "turso.tech/database/tursogo"
)

// Options tunes how a Turso database file is opened.
type Options struct {
	// BusyTimeout is how long a statement waits on a lock held by another connection or
	// process before failing. Zero keeps the driver default of 5 seconds.
	BusyTimeout time.Duration

	// Experimental lists Turso experimental features passed to the engine, for example
	// "multiprocess_wal". Read the Turso docs before enabling any of them.
	Experimental []string

	// MaxOpenConns caps the database/sql pool. Zero leaves it unlimited.
	MaxOpenConns int

	// MustExist makes Open fail with fs.ErrNotExist instead of creating a missing file.
	// Use it for tenant databases, so a typo never silently creates an empty database.
	MustExist bool
}

// Open opens the Turso database file at path and returns a pooled *sql.DB.
//
// PRAGMA foreign_keys = ON is executed on every new pooled connection. Turso, like SQLite,
// defaults it to OFF per connection and the driver has no DSN option for it, so without this
// the foreign keys and cascades in the migrations would silently not be enforced.
func Open(ctx context.Context, path string, o Options) (*sql.DB, error) {
	if o.MustExist {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("db: %w", err)
		}
	} else if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("db: create directory for %s: %w", path, err)
	}

	dsn := path
	if len(o.Experimental) > 0 {
		dsn += "?experimental=" + url.QueryEscape(strings.Join(o.Experimental, ","))
	}

	var copts []turso.ConnectorOption
	if o.BusyTimeout > 0 {
		copts = append(copts, turso.WithBusyTimeout(int(o.BusyTimeout.Milliseconds())))
	}
	inner, err := turso.NewConnector(dsn, copts...)
	if err != nil {
		return nil, fmt.Errorf("db: connector for %s: %w", path, err)
	}

	sqlDB := sql.OpenDB(&fkConnector{inner: inner})
	if o.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(o.MaxOpenConns)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}
	return sqlDB, nil
}

// NewDBX wraps sqlDB for dbx.
//
// pocketbase/dbx chooses its SQL builder from the driver name, and "sqlite" selects the
// SQLite builder. dbx.NewFromDB returns a single value, there is no error to handle.
// The caller owns sqlDB: closing the returned *dbx.DB closes it.
func NewDBX(sqlDB *sql.DB) *dbx.DB {
	return dbx.NewFromDB(sqlDB, "sqlite")
}

// ResolvePath turns a path stored in the control plane into a real file path. Relative
// paths are joined onto dataDir, so moving the data volume does not invalidate rows.
func ResolvePath(dataDir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dataDir, p)
}

// IsNotExist reports whether err means the database file is missing.
func IsNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }

// fkConnector wraps the turso connector and enables foreign keys on each new connection.
type fkConnector struct{ inner driver.Connector }

func (c *fkConnector) Driver() driver.Driver { return c.inner.Driver() }

func (c *fkConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	ex, ok := conn.(driver.ExecerContext)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("db: turso connection does not implement driver.ExecerContext")
	}
	if _, err := ex.ExecContext(ctx, "PRAGMA foreign_keys = ON", nil); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("db: enable foreign keys: %w", err)
	}
	return conn, nil
}
