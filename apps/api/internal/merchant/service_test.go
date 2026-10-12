package merchant

// These tests use the real Turso driver and real migrations in a temporary directory.
// Run them with: go test ./internal/merchant -v -race -count=1

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/pocketbase/dbx"

	"encore.app/internal/db"
	"encore.app/internal/dbmigrate"
	"encore.app/internal/tenantdb"
	"encore.app/migrations"
)

var testOpts = db.Options{BusyTimeout: 10 * time.Second, MaxOpenConns: 1}

// newService returns a Service on a fresh data directory with the control-plane schema applied.
func newService(t *testing.T, o Options) (*MerchantController, *dbx.DB, string) {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := db.Open(context.Background(), filepath.Join(dir, "control.db"), testOpts)
	if err != nil {
		t.Fatal(err)
	}
	m, err := dbmigrate.New(migrations.FS, dbmigrate.Control, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatal(err)
	}
	// Closing the migrator closes sqlDB, so open the handle the service will use afterwards.
	if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
		t.Fatal(srcErr, dbErr)
	}
	sqlDB, err = db.Open(context.Background(), filepath.Join(dir, "control.db"), testOpts)
	if err != nil {
		t.Fatal(err)
	}
	control := db.NewDBX(sqlDB)
	t.Cleanup(func() { _ = control.Close() })

	o.DB = testOpts
	return New(control, dir, o), control, dir
}

func count(t *testing.T, c *dbx.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := c.DB().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func TestCreateProvisionsMerchant(t *testing.T) {
	ctx := context.Background()
	s, control, dir := newService(t, Options{})
	latest, err := dbmigrate.Latest(migrations.FS, dbmigrate.Tenant)
	if err != nil {
		t.Fatal(err)
	}

	m, err := s.Create(
		ctx,
		Input{UserID: "user-1", Name: "  Phở Bà Tư ", Slug: "Pho-Ba-Tu", Currency: "vnd"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if m.Slug != "pho-ba-tu" || m.Name != "Phở Bà Tư" || m.SchemaVersion != latest {
		t.Fatalf("unexpected result %+v", m)
	}

	var status string
	var version int
	var rel string
	if err := control.DB().QueryRow(
		`SELECT status, schema_version, db_path FROM tenant_databases WHERE merchant_id = ?`, m.ID).
		Scan(&status, &version, &rel); err != nil {
		t.Fatal(err)
	}
	if status != "ready" || version != int(latest) || rel != "tenants/"+m.ID+".db" {
		t.Fatalf("tenant_databases: status=%s version=%d path=%s", status, version, rel)
	}
	if n := count(t, control, `SELECT COUNT(*) FROM merchant_members WHERE merchant_id = ? AND user_id = 'user-1' AND role = 'owner'`, m.ID); n != 1 {
		t.Fatalf("owner membership rows = %d", n)
	}

	// The merchant database opens through the same path the API uses, and holds the profile.
	_ = control.Close() // hand the control file over: the manager is its only owner from here on
	mgr, err := tenantdb.New(ctx, dir, "control.db", testOpts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	h, err := mgr.Tenant(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	var name, currency, tz, mid string
	if err := h.DB().QueryRow(`SELECT merchant_id, name, currency, timezone FROM merchant_profile WHERE id = 1`).
		Scan(&mid, &name, &currency, &tz); err != nil {
		t.Fatal(err)
	}
	if mid != m.ID || name != "Phở Bà Tư" || currency != "VND" || tz != "Asia/Ho_Chi_Minh" {
		t.Fatalf("profile = %s %s %s %s", mid, name, currency, tz)
	}
	if n := count(t, h, `SELECT COUNT(*) FROM branches`); n != 0 {
		t.Fatalf("a new merchant should have no branches, has %d", n)
	}
}

func TestCreateValidation(t *testing.T) {
	s, control, _ := newService(t, Options{})
	long := strings.Repeat("a", 41)
	tests := []struct {
		name string
		in   Input
		want string // field
	}{
		{"no user", Input{Name: "A", Slug: "abc"}, "user"},
		{"no name", Input{UserID: "u", Slug: "abc"}, "name"},
		{"name too long", Input{UserID: "u", Name: strings.Repeat("x", 81), Slug: "abc"}, "name"},
		{"control char", Input{UserID: "u", Name: "a\x00b", Slug: "abc"}, "name"},
		{"short slug", Input{UserID: "u", Name: "A", Slug: "ab"}, "slug"},
		{"long slug", Input{UserID: "u", Name: "A", Slug: long}, "slug"},
		{"slug chars", Input{UserID: "u", Name: "A", Slug: "pho_ba"}, "slug"},
		{"slug edge hyphen", Input{UserID: "u", Name: "A", Slug: "-pho"}, "slug"},
		{"slug double hyphen", Input{UserID: "u", Name: "A", Slug: "pho--ba"}, "slug"},
		{"slug reserved", Input{UserID: "u", Name: "A", Slug: "admin"}, "slug"},
		{"currency", Input{UserID: "u", Name: "A", Slug: "abc", Currency: "dong"}, "currency"},
		{"timezone", Input{UserID: "u", Name: "A", Slug: "abc", Timezone: "Mars/Base"}, "timezone"},
		{
			"timezone local",
			Input{UserID: "u", Name: "A", Slug: "abc", Timezone: "Local"},
			"timezone",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.Create(context.Background(), tt.in)
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Field != tt.want {
				t.Fatalf("got %v, want a validation error on %q", err, tt.want)
			}
		})
	}
	if n := count(t, control, `SELECT COUNT(*) FROM merchants`); n != 0 {
		t.Fatalf("validation failures wrote %d merchants", n)
	}
}

func TestCreateSlugTakenLeavesNothingBehind(t *testing.T) {
	ctx := context.Background()
	s, control, dir := newService(t, Options{})
	if _, err := s.Create(ctx, Input{UserID: "u1", Name: "One", Slug: "same"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, Input{UserID: "u2", Name: "Two", Slug: "SAME"}); !errors.Is(
		err,
		ErrSlugTaken,
	) {
		t.Fatalf("got %v, want ErrSlugTaken", err)
	}
	if n := count(t, control, `SELECT COUNT(*) FROM merchants`); n != 1 {
		t.Fatalf("merchants = %d, want 1", n)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "tenants", "*.db"))
	if len(files) != 1 {
		t.Fatalf("tenant files = %v, want exactly one", files)
	}
}

func TestCreateOwnerLimit(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newService(t, Options{MaxOwned: 2})
	for i := 0; i < 2; i++ {
		if _, err := s.Create(ctx, Input{UserID: "u", Name: "S", Slug: fmt.Sprintf("shop-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Create(ctx, Input{UserID: "u", Name: "S", Slug: "shop-3"}); !errors.Is(
		err,
		ErrLimitReached,
	) {
		t.Fatalf("got %v, want ErrLimitReached", err)
	}
	if _, err := s.Create(ctx, Input{UserID: "other", Name: "S", Slug: "shop-3"}); err != nil {
		t.Fatalf("another user must not be limited: %v", err)
	}
}

func TestCreateFailureRollsBackAndFreesSlug(t *testing.T) {
	ctx := context.Background()
	s, control, dir := newService(t, Options{})
	s.afterCopy = func() error { return errors.New("injected failure") }

	if _, err := s.Create(ctx, Input{UserID: "u", Name: "Shop", Slug: "retry-me"}); err == nil ||
		!strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("got %v, want the injected failure", err)
	}
	if n := count(t, control, `SELECT COUNT(*) FROM merchants`); n != 0 {
		t.Fatalf("merchants = %d after rollback", n)
	}
	if n := count(t, control, `SELECT COUNT(*) FROM tenant_databases`) + count(t, control, `SELECT COUNT(*) FROM merchant_members`); n != 0 {
		t.Fatalf("cascade left %d rows", n)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "tenants", "*")); len(files) != 0 {
		t.Fatalf("leftover files %v", files)
	}

	s.afterCopy = nil
	if _, err := s.Create(ctx, Input{UserID: "u", Name: "Shop", Slug: "retry-me"}); err != nil {
		t.Fatalf("slug should be free again: %v", err)
	}
}

func TestTemplateBuiltOnceAndReused(t *testing.T) {
	ctx := context.Background()
	s, _, dir := newService(t, Options{})
	for i := 0; i < 3; i++ {
		if _, err := s.Create(ctx, Input{UserID: fmt.Sprintf("u%d", i), Name: "S", Slug: fmt.Sprintf("shop-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	tpls, _ := filepath.Glob(filepath.Join(dir, "templates", "*"))
	if len(tpls) != 1 || !strings.HasSuffix(tpls[0], ".db") {
		t.Fatalf("templates = %v, want a single finished file", tpls)
	}
}

func TestCreateConcurrent(t *testing.T) {
	ctx := context.Background()
	s, control, dir := newService(t, Options{MaxOwned: -1})

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Create(ctx, Input{UserID: "u", Name: "S", Slug: fmt.Sprintf("shop-%d", i)})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if got := count(t, control, `SELECT COUNT(*) FROM tenant_databases WHERE status = 'ready'`); got != n {
		t.Fatalf("ready tenants = %d, want %d", got, n)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "tenants", "*.db")); len(files) != n {
		t.Fatalf("tenant files = %d, want %d", len(files), n)
	}
}

func TestConcurrentSameSlugOnlyOneWins(t *testing.T) {
	ctx := context.Background()
	s, control, _ := newService(t, Options{MaxOwned: -1})

	const n = 6
	var wg sync.WaitGroup
	results := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Create(
				ctx,
				Input{UserID: fmt.Sprintf("u%d", i), Name: "S", Slug: "contested"},
			)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	won := 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrSlugTaken):
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d creations succeeded, want exactly 1", won)
	}
	if got := count(t, control, `SELECT COUNT(*) FROM merchants WHERE slug = 'contested'`); got != 1 {
		t.Fatalf("rows = %d", got)
	}
}

func TestListForUser(t *testing.T) {
	ctx := context.Background()
	s, control, _ := newService(t, Options{})
	a, _ := s.Create(ctx, Input{UserID: "u", Name: "A", Slug: "shop-a"})
	if _, err := s.Create(ctx, Input{UserID: "u", Name: "B", Slug: "shop-b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, Input{UserID: "other", Name: "C", Slug: "shop-c"}); err != nil {
		t.Fatal(err)
	}
	// A suspended merchant disappears from the list.
	if _, err := control.DB().Exec(`UPDATE merchants SET status = 'suspended' WHERE id = ?`, a.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListForUser(ctx, "u")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Slug != "shop-b" || got[0].Role != "owner" {
		t.Fatalf("list = %+v", got)
	}
	if none, err := s.ListForUser(ctx, "nobody"); err != nil || len(none) != 0 {
		t.Fatalf("list for unknown user = %v, %v", none, err)
	}
}

func TestReapStale(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, control, dir := newService(t, Options{Now: func() time.Time { return now }})

	ready, err := s.Create(ctx, Input{UserID: "u", Name: "Live", Slug: "live"})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a crash after the control-plane commit: rows exist, status provisioning, file half there.
	old := now.Add(-time.Hour).Unix()
	insert := func(id, slug, status string, updated int64) {
		t.Helper()
		for _, q := range []string{
			fmt.Sprintf(`INSERT INTO merchants (id, slug, name, created_by, created_at, updated_at) VALUES ('%[1]s', '%[2]s', 'x', 'u', %[3]d, %[3]d)`, id, slug, updated),
			fmt.Sprintf(`INSERT INTO tenant_databases (merchant_id, shard_id, db_path, status, created_at, updated_at) VALUES ('%[1]s', 's1', 'tenants/%[1]s.db', '%[2]s', %[3]d, %[3]d)`, id, status, updated),
		} {
			if _, err := control.DB().Exec(q); err != nil {
				t.Fatal(err)
			}
		}
	}
	insert("crashed", "crashed", "provisioning", old)
	insert("failed-old", "failed-old", "failed", old)
	insert("in-flight", "in-flight", "provisioning", now.Unix())
	if err := os.MkdirAll(filepath.Join(dir, "tenants"), 0o750); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(dir, "tenants", "crashed.db")
	if err := os.WriteFile(partial, []byte("partial"), 0o640); err != nil {
		t.Fatal(err)
	}
	// A hostile or corrupt path must be refused, not followed.
	if _, err := control.DB().Exec(`UPDATE tenant_databases SET db_path = '../outside.db' WHERE merchant_id = 'failed-old'`); err != nil {
		t.Fatal(err)
	}

	n, err := s.ReapStale(ctx, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reaped %d, want 1 (the unsafe path is skipped)", n)
	}
	if exists(partial) {
		t.Fatal("partial file was not removed")
	}
	for id, want := range map[string]int{"crashed": 0, "failed-old": 1, "in-flight": 1, ready.ID: 1} {
		if got := count(t, control, `SELECT COUNT(*) FROM merchants WHERE id = ?`, id); got != want {
			t.Errorf("merchant %s rows = %d, want %d", id, got, want)
		}
	}
}
