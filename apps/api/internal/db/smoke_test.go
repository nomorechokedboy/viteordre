package db_test

// Smoke test for the "one Manager per service" model: several independent handles on the same
// Turso file inside ONE process, which is what N Encore services in one container would do.
// It needs the real tursogo driver, so run it with: go test ./internal/db -run Smoke -v -race -count=1

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pocketbase/dbx"

	"encore.app/internal/db"
)

func openHandle(t *testing.T, path string) *dbx.DB {
	t.Helper()
	sqlDB, err := db.Open(context.Background(), path, db.Options{
		BusyTimeout:  10 * time.Second,
		MaxOpenConns: 4, // several pooled connections per handle, like a busy service
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return db.NewDBX(sqlDB)
}

func count(t *testing.T, h *dbx.DB, table string) int {
	t.Helper()
	var n int
	if err := h.NewQuery("SELECT COUNT(*) FROM " + table).Row(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestSmokeTwoHandlesOneFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "merchant.db")
	a := openHandle(t, path) // "service A"
	b := openHandle(t, path) // "service B"

	for _, q := range []string{
		"CREATE TABLE parent (id TEXT PRIMARY KEY) STRICT",
		"CREATE TABLE child (id TEXT PRIMARY KEY, parent_id TEXT NOT NULL REFERENCES parent (id)) STRICT",
		"INSERT INTO parent VALUES ('p')",
	} {
		if _, err := a.NewQuery(q).Execute(); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	t.Run("sees the other handle's writes", func(t *testing.T) {
		if _, err := a.NewQuery("INSERT INTO child VALUES ('seen', 'p')").Execute(); err != nil {
			t.Fatal(err)
		}
		if n := count(t, b, "child"); n != 1 {
			t.Fatalf("handle B sees %d rows, want 1", n)
		}
	})

	t.Run("foreign keys enforced on both handles", func(t *testing.T) {
		for name, h := range map[string]*dbx.DB{"A": a, "B": b} {
			for i := 0; i < 8; i++ { // several pooled connections
				if _, err := h.NewQuery("INSERT INTO child VALUES ({:id}, 'missing')").
					Bind(dbx.Params{"id": fmt.Sprintf("bad-%s-%d", name, i)}).Execute(); err == nil {
					t.Fatalf("handle %s accepted a row with a missing parent", name)
				}
			}
		}
	})

	t.Run("concurrent writers on both handles", func(t *testing.T) {
		const perWorker, workers = 100, 4 // per handle
		var wg sync.WaitGroup
		errs := make(chan error, 2*workers*perWorker)
		for name, h := range map[string]*dbx.DB{"A": a, "B": b} {
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func(name string, h *dbx.DB, w int) {
					defer wg.Done()
					for i := 0; i < perWorker; i++ {
						tx, err := h.Begin()
						if err != nil {
							errs <- fmt.Errorf("%s begin: %w", name, err)
							continue
						}
						_, err = tx.NewQuery("INSERT INTO child VALUES ({:id}, 'p')").
							Bind(dbx.Params{"id": fmt.Sprintf("%s-%d-%d", name, w, i)}).Execute()
						if err != nil {
							_ = tx.Rollback()
							errs <- fmt.Errorf("%s insert: %w", name, err)
							continue
						}
						if err := tx.Commit(); err != nil {
							errs <- fmt.Errorf("%s commit: %w", name, err)
						}
					}
				}(name, h, w)
			}
		}
		wg.Wait()
		close(errs)
		failed := 0
		for err := range errs {
			if failed < 5 {
				t.Errorf("write error: %v", err)
			}
			failed++
		}
		want := 1 + 2*workers*perWorker // 'seen' plus every concurrent insert
		if failed > 0 {
			t.Errorf("%d of %d writes failed", failed, 2*workers*perWorker)
		}
		if na, nb := count(t, a, "child"), count(t, b, "child"); na != want || nb != want {
			t.Errorf("rows seen by A=%d, B=%d, want %d", na, nb, want)
		}
	})

	t.Run("close order does not matter", func(t *testing.T) {
		if err := a.Close(); err != nil {
			t.Fatalf("close A: %v", err)
		}
		if n := count(t, b, "parent"); n != 1 { // B must still work
			t.Fatalf("B after closing A sees %d parents", n)
		}
		if err := b.Close(); err != nil {
			t.Fatalf("close B: %v", err)
		}
	})

	t.Run("data survives reopen", func(t *testing.T) {
		c := openHandle(t, path)
		defer c.Close()
		if n := count(t, c, "child"); n < 1 {
			t.Fatalf("after reopen: %d rows", n)
		}
	})
}
