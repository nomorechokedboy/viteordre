package tenantdb

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/pocketbase/dbx"

	"encore.app/internal/db"
)

func testConfig(dir string) Config {
	return Config{DataDir: dir, ControlFile: "control.db", Options: db.Options{BusyTimeout: 5 * time.Second, MaxOpenConns: 1}}
}

func TestDefaultSharesOneManagerAndClosesOnLastRelease(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t.TempDir())

	a, err := Default(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Default(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("Default returned two Managers")
	}

	other := cfg
	other.ControlFile = "other.db"
	if _, err := Default(ctx, other); err == nil {
		t.Fatal("a different Config should be an error")
	}

	if err := a.Release(); err != nil {
		t.Fatal(err)
	}
	if a.closed {
		t.Fatal("closed while another service still holds it")
	}
	if err := b.Release(); err != nil {
		t.Fatal(err)
	}
	if !a.closed {
		t.Fatal("not closed after the last Release")
	}
	if err := b.Release(); err != nil { // extra release is harmless
		t.Fatal(err)
	}

	c, err := Default(ctx, cfg) // a new one can be opened afterwards
	if err != nil {
		t.Fatal(err)
	}
	if c == a {
		t.Fatal("expected a fresh Manager after the last Release")
	}
	_ = c.Release()
}

func TestTenantOpensReadyAndMigratingOnly(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	m, err := New(ctx, dir, "control.db", testConfig(dir).Options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })

	if _, err := m.control.NewQuery(`CREATE TABLE tenant_databases (merchant_id TEXT PRIMARY KEY, db_path TEXT NOT NULL, status TEXT NOT NULL)`).Execute(); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"ready", "migrating", "provisioning", "failed", "archived"} {
		path := "tenants/" + status + ".db"
		sqlDB, err := db.Open(ctx, filepath.Join(dir, path), db.Options{})
		if err != nil {
			t.Fatal(err)
		}
		_ = sqlDB.Close()
		if _, err := m.control.NewQuery(`INSERT INTO tenant_databases VALUES ({:id}, {:path}, {:status})`).
			Bind(dbx.Params{"id": status, "path": path, "status": status}).Execute(); err != nil {
			t.Fatal(err)
		}
	}

	for _, status := range []string{"ready", "migrating"} {
		if _, err := m.Tenant(ctx, status); err != nil {
			t.Errorf("%s: %v", status, err)
		}
	}
	for _, status := range []string{"provisioning", "failed", "archived"} {
		if _, err := m.Tenant(ctx, status); !errors.Is(err, ErrTenantUnavailable) {
			t.Errorf("%s: err = %v, want ErrTenantUnavailable", status, err)
		}
	}
	if _, err := m.Tenant(ctx, "nobody"); !errors.Is(err, ErrTenantUnavailable) {
		t.Errorf("unknown merchant: err = %v", err)
	}
}
