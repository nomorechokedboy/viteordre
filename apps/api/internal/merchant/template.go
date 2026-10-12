package merchant

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"

	"encore.app/internal/apperr"
	"encore.app/internal/db"
	"encore.app/internal/dbmigrate"
	"encore.app/internal/logger"
)

// ensureTemplate returns the path of a merchant database that has every tenant migration
// applied and nothing else in it, building it on first use.
//
// The file name carries the schema version, so shipping a new tenant migration makes the
// next call build a fresh template and leaves older ones untouched. A template is never
// modified after it is renamed into place.
func (s *MerchantController) ensureTemplate(
	ctx context.Context,
) (path string, version uint, err error) {
	version, err = dbmigrate.Latest(s.fsys, dbmigrate.Tenant)
	if err != nil {
		logger.ErrorContext(ctx, "dbmigrate.Latest error", "err", err)
		return "", 0, err
	}
	path = filepath.Join(s.dataDir, "templates", fmt.Sprintf("tenant-%06d.db", version))

	s.tplMu.Lock()
	defer s.tplMu.Unlock()

	if _, err := os.Stat(path); err == nil {
		return path, version, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", 0, apperr.Wrap(err, apperr.NotFound, "File does not exist")
	}

	tmp := path + ".building"
	removeDBFiles(tmp)
	defer removeDBFiles(tmp) // a no-op after the rename below

	opts := s.dbOpts
	opts.MustExist = false
	sqlDB, err := db.Open(ctx, tmp, opts)
	if err != nil {
		logger.ErrorContext(ctx, "build template error", "err", err)
		return "", 0, apperr.Newf(apperr.Internal, "build template: %w", err)
	}
	m, err := dbmigrate.New(s.fsys, dbmigrate.Tenant, sqlDB)
	if err != nil {
		_ = sqlDB.Close()
		logger.ErrorContext(ctx, "tenant migrate failed", "err", err)
		return "", 0, apperr.Newf(apperr.Internal, "build template: %w", err)
	}
	upErr := m.Up()
	if errors.Is(upErr, migrate.ErrNoChange) {
		upErr = nil
	}
	got, dirty, vErr := m.Version()
	srcErr, dbErr := m.Close() // also closes sqlDB
	if err := errors.Join(upErr, vErr, srcErr, dbErr); err != nil {
		return "", 0, apperr.Newf(apperr.Internal, "build template: %w", err)
	}
	if dirty || got != version {
		logger.ErrorContext(ctx, "buid template error", "version", got, "dirty", dirty, "want", version)
		return "", 0, apperr.Newf(
			apperr.Internal,
			"build template: ended at version %d dirty=%v, want %d",
			got,
			dirty,
			version,
		)
	}

	// A copy must capture everything, so no write-ahead data may be left outside the file.
	if err := requireNoSideData(tmp); err != nil {
		logger.ErrorContext(ctx, "requireNoSideData error", "err", err)
		return "", 0, apperr.Newf(apperr.Internal, "build template: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", 0, apperr.Wrap(err, apperr.Internal, "MkdirAll error")
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", 0, apperr.Newf(apperr.Internal, "build template: %w", err)
	}
	return path, version, nil
}

// sideSuffixes are the files a Turso database may keep beside the main file.
var sideSuffixes = []string{"-wal", "-shm", "-log"}

func removeDBFiles(path string) {
	_ = os.Remove(path)
	for _, suf := range sideSuffixes {
		_ = os.Remove(path + suf)
	}
}

// requireNoSideData fails when a side file still holds data. Empty side files are removed.
func requireNoSideData(path string) error {
	for _, suf := range sideSuffixes {
		fi, err := os.Stat(path + suf)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if fi.Size() > 0 {
			return apperr.Newf(
				apperr.Internal,
				"%s still holds %d bytes after close, so a plain file copy would lose data",
				filepath.Base(path+suf),
				fi.Size(),
			)
		}
		if err := os.Remove(path + suf); err != nil {
			logger.Error("Remove files error", "err", err)
			return apperr.Wrap(err, apperr.Internal, "Remove files error")
		}
	}
	return nil
}

// copyFile copies src to dst through a temporary file, so dst either holds the full copy or
// does not exist. It refuses to overwrite.
func copyFile(src, dst string) (err error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	part := dst + ".part"
	out, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = out.Close()
			_ = os.Remove(part)
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	if err = os.Rename(part, dst); err != nil {
		return err
	}
	// Persist the rename. Failing to open the directory is not fatal, some filesystems
	// do not allow it.
	if d, derr := os.Open(filepath.Dir(dst)); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
