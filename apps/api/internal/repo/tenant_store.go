package repo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

	tplMu sync.Mutex // serializes building the template
}

// NewTenantStore returns a store that keeps merchant databases in dataDir/tenants and templates in
// dataDir/templates. fsys holds the migrations, nil means the embedded set. opts is how the
// files are opened.
func NewTenantStore(dataDir string, fsys fs.FS, opts db.Options) *TenantStore {
	if fsys == nil {
		fsys = migrations.FS
	}
	return &TenantStore{dataDir: dataDir, opts: opts, fsys: fsys}
}

// EnsureTemplate returns a merchant database that has every tenant migration applied and nothing
// else in it, building it on first use.
//
// The file name carries the schema version, so shipping a new tenant migration makes the next call
// build a fresh template and leaves older ones untouched. A template is never modified after it is
// renamed into place.
func (s *TenantStore) EnsureTemplate(ctx context.Context) (entities.TenantTemplate, error) {
	version, err := dbmigrate.Latest(s.fsys, dbmigrate.Tenant)
	if err != nil {
		return entities.TenantTemplate{}, internal("find latest tenant migration", err)
	}
	path := filepath.Join(s.dataDir, "templates", fmt.Sprintf("tenant-%06d.db", version))
	tpl := entities.TenantTemplate{Path: path, Version: version}

	s.tplMu.Lock()
	defer s.tplMu.Unlock()

	if _, err := os.Stat(path); err == nil {
		return tpl, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return entities.TenantTemplate{}, internal("stat template", err)
	}

	tmp := path + ".building"
	removeDBFiles(tmp)
	defer removeDBFiles(tmp) // a no-op after the rename below

	opts := s.opts
	opts.MustExist = false
	sqlDB, err := db.Open(ctx, tmp, opts)
	if err != nil {
		return entities.TenantTemplate{}, internal("open template database", err)
	}
	m, err := dbmigrate.New(s.fsys, dbmigrate.Tenant, sqlDB)
	if err != nil {
		_ = sqlDB.Close()
		return entities.TenantTemplate{}, internal("prepare template migrations", err)
	}
	upErr := m.Up()
	if errors.Is(upErr, migrate.ErrNoChange) {
		upErr = nil
	}
	got, dirty, vErr := m.Version()
	srcErr, dbErr := m.Close() // also closes sqlDB
	if err := errors.Join(upErr, vErr, srcErr, dbErr); err != nil {
		return entities.TenantTemplate{}, internal("migrate template", err)
	}
	if dirty || got != version {
		return entities.TenantTemplate{}, internal("migrate template",
			fmt.Errorf("ended at version %d dirty=%v, want %d", got, dirty, version))
	}

	// A copy must capture everything, so no write-ahead data may be left outside the file.
	if err := requireNoSideData(tmp); err != nil {
		return entities.TenantTemplate{}, internal("seal template", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return entities.TenantTemplate{}, internal("create template directory", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return entities.TenantTemplate{}, internal("publish template", err)
	}
	return tpl, nil
}

// Provision copies the template to dbPath (relative to the data directory), checks that the copy is
// at the template's schema version and writes the merchant_profile row.
//
// A failure can leave a partial file behind. The caller removes it with Discard.
func (s *TenantStore) Provision(
	ctx context.Context,
	tpl entities.TenantTemplate,
	dbPath string,
	p entities.TenantProfile,
) error {
	dst := db.ResolvePath(s.dataDir, dbPath)
	if err := copyFile(tpl.Path, dst); err != nil {
		return internal("copy template", err)
	}
	return s.initProfile(ctx, dst, tpl.Version, p)
}

// initProfile confirms the copied database really is at the expected schema version and writes
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
				fmt.Errorf("copied database is at version %d dirty=%v, want %d", got, dirty, version))
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
