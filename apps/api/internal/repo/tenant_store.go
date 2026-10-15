package repo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	"github.com/pocketbase/dbx"

	"encore.app/internal/apperr"
	"encore.app/internal/db"
	"encore.app/internal/dbmigrate"
	"encore.app/internal/entities"
	"encore.app/migrations"
)

// TenantStore creates and removes merchant database files below the data directory.
type TenantStore struct {
	dataDir string
	opts    db.Options
	fsys    fs.FS
}

// NewTenantStore returns a store that keeps merchant databases in dataDir/tenants. fsys holds the
// migrations, nil means the embedded set. opts is how the files are opened.
func NewTenantStore(dataDir string, fsys fs.FS, opts db.Options) *TenantStore {
	if fsys == nil {
		fsys = migrations.FS
	}
	return &TenantStore{dataDir: dataDir, opts: opts, fsys: fsys}
}

// Provision creates the merchant database at dbPath (relative to the data directory): it applies
// every tenant migration to a new file, checks the result and writes the merchant_profile row. It
// returns the schema version the database ended at.
//
// Each merchant is migrated in place rather than copied from a prebuilt file. Turso keeps recent
// writes in a write-ahead log beside the main file and does not fold them back on close, so
// copying only the main file of a freshly migrated database yields an empty database. The engine
// itself always reads the log, so a database it wrote is complete when it opens it again.
//
// A failure can leave a partial file behind. The caller removes it with Discard.
func (s *TenantStore) Provision(ctx context.Context, dbPath string, p entities.TenantProfile) (uint, error) {
	dst := db.ResolvePath(s.dataDir, dbPath)
	if _, err := os.Stat(dst); err == nil {
		return 0, internal("create merchant database", fmt.Errorf("%s already exists", dst))
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, internal("stat merchant database", err)
	}
	version, err := dbmigrate.Latest(s.fsys, dbmigrate.Tenant)
	if err != nil {
		return 0, internal("find latest tenant migration", err)
	}
	if err := s.migrate(ctx, dst, version); err != nil {
		return 0, err
	}
	if err := s.initProfile(ctx, dst, version, p); err != nil {
		return 0, err
	}
	return version, nil
}

// migrate creates the file at path and applies every tenant migration, which must end at version.
func (s *TenantStore) migrate(ctx context.Context, path string, version uint) error {
	opts := s.opts
	opts.MustExist = false
	sqlDB, err := db.Open(ctx, path, opts)
	if err != nil {
		return internal("open merchant database", err)
	}
	m, err := dbmigrate.New(s.fsys, dbmigrate.Tenant, sqlDB)
	if err != nil {
		_ = sqlDB.Close()
		return internal("prepare merchant migrations", err)
	}
	upErr := m.Up()
	if errors.Is(upErr, migrate.ErrNoChange) {
		upErr = nil
	}
	got, dirty, vErr := m.Version()
	srcErr, dbErr := m.Close() // also closes sqlDB
	if err := errors.Join(upErr, vErr, srcErr, dbErr); err != nil {
		return internal("migrate merchant database", err)
	}
	if dirty || got != version {
		return internal("migrate merchant database",
			fmt.Errorf("ended at version %d dirty=%v, want %d", got, dirty, version))
	}
	return nil
}

// initProfile confirms the new database really is at the expected schema version and writes
// its single merchant_profile row.
func (s *TenantStore) initProfile(
	ctx context.Context,
	file string,
	version uint,
	p entities.TenantProfile,
) (err error) {
	opts := s.opts
	opts.MustExist = true
	sqlDB, err := db.Open(ctx, file, opts)
	if err != nil {
		return internal("open merchant database", err)
	}
	h := db.NewDBX(sqlDB)
	defer func() {
		if cerr := h.Close(); err == nil && cerr != nil {
			err = internal("close merchant database", cerr)
		}
	}()

	err = h.TransactionalContext(ctx, nil, func(tx *dbx.Tx) error {
		var got int64
		var dirty bool
		if err := tx.NewQuery(`SELECT version, dirty FROM schema_migrations`).
			WithContext(ctx).Row(&got, &dirty); err != nil {
			return internal("read schema version", err)
		}
		if dirty || got != int64(version) {
			return internal("check schema version",
				fmt.Errorf("merchant database is at version %d dirty=%v, want %d", got, dirty, version))
		}
		at := p.At.Unix()
		if _, err := tx.NewQuery(
			`INSERT INTO merchant_profile (id, merchant_id, name, currency, timezone, created_at, updated_at)
			 VALUES (1, {:id}, {:name}, {:currency}, {:tz}, {:at}, {:at})`).
			WithContext(ctx).
			Bind(dbx.Params{
				"id": p.MerchantID, "name": p.Name, "currency": p.Currency, "tz": p.Timezone, "at": at,
			}).Execute(); err != nil {
			return internal("insert merchant profile", err)
		}
		return nil
	})
	return internal("initialize merchant database", err)
}

// ErrUnsafePath is returned by Discard for a path that does not point into the tenants directory.
var ErrUnsafePath = apperr.New(apperr.Internal, "refusing to touch an unexpected database path")

// Discard removes the database file at dbPath and its side files. A missing file is fine.
//
// The path usually comes from the control-plane database, so anything that is not a plain
// tenants/<name> path is refused with ErrUnsafePath and nothing is deleted.
func (s *TenantStore) Discard(dbPath string) error {
	if !filepath.IsLocal(filepath.FromSlash(dbPath)) || !strings.HasPrefix(dbPath, "tenants/") {
		return ErrUnsafePath
	}
	removeDBFiles(db.ResolvePath(s.dataDir, dbPath))
	return nil
}
