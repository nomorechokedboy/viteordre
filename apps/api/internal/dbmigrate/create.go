package dbmigrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	fileRe     = regexp.MustCompile(`^(\d+)_.*\.(up|down)\.sql$`)
	nonWordRe  = regexp.MustCompile(`[^a-z0-9]+`)
	versionPad = 6
)

// CreateFiles writes an empty NNNNNN_name.up.sql and .down.sql pair into dir/kind, numbered
// one above the highest existing version. dir is the directory that holds control and tenant,
// normally the migrations directory of the API. It returns the two file paths.
func CreateFiles(dir string, kind Kind, name string) (up, down string, err error) {
	slug := strings.Trim(nonWordRe.ReplaceAllString(strings.ToLower(name), "_"), "_")
	if slug == "" {
		return "", "", errors.New("migration name must contain letters or digits")
	}

	target := filepath.Join(dir, string(kind))
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", "", err
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return "", "", err
	}
	var highest uint64
	for _, e := range entries {
		if m := fileRe.FindStringSubmatch(e.Name()); m != nil {
			if v, err := strconv.ParseUint(m[1], 10, 64); err == nil && v > highest {
				highest = v
			}
		}
	}

	version := fmt.Sprintf("%0*d", versionPad, highest+1)
	up = filepath.Join(target, fmt.Sprintf("%s_%s.up.sql", version, slug))
	down = filepath.Join(target, fmt.Sprintf("%s_%s.down.sql", version, slug))

	header := func(direction string) []byte {
		return []byte(fmt.Sprintf("-- %s migration %s %s (%s)\n", kind, version, slug, direction))
	}
	for path, direction := range map[string]string{up: "up", down: "down"} {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return "", "", err
		}
		if _, err := f.Write(header(direction)); err != nil {
			_ = f.Close()
			return "", "", err
		}
		if err := f.Close(); err != nil {
			return "", "", err
		}
	}
	return up, down, nil
}
