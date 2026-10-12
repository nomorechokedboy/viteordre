package repo

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

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
			return fmt.Errorf("%s still holds %d bytes after close, so a plain file copy would lose data",
				filepath.Base(path+suf), fi.Size())
		}
		if err := os.Remove(path + suf); err != nil {
			return err
		}
	}
	return nil
}

// copyFile copies src to dst through a temporary file, so dst either holds the full copy or does
// not exist. It refuses to overwrite.
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
	// Persist the rename. Failing to open the directory is not fatal, some filesystems do not
	// allow it.
	if d, derr := os.Open(filepath.Dir(dst)); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
