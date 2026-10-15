package repo

import "os"

// sideSuffixes are the files a Turso database may keep beside the main file.
var sideSuffixes = []string{"-wal", "-shm", "-log"}

// removeDBFiles deletes a database file and its side files. Missing files are fine.
func removeDBFiles(path string) {
	_ = os.Remove(path)
	for _, suf := range sideSuffixes {
		_ = os.Remove(path + suf)
	}
}
