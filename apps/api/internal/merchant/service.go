// Package merchant creates merchants: it registers them in the control plane, gives each one
// its own database file cloned from a template, and lists the merchants a user belongs to.
package merchant

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pocketbase/dbx"

	"encore.app/internal/apperr"
	"encore.app/internal/db"
	"encore.app/internal/entities"
	"encore.app/internal/logger"
	"encore.app/migrations"
)

var (
	// ErrSlugTaken means another merchant already uses the slug.
	ErrSlugTaken = apperr.New(apperr.Conflict, "merchant: slug is already taken")
	// ErrLimitReached means the user already owns the maximum number of merchants.
	ErrLimitReached = apperr.New(apperr.FailedPrecondition, "merchant: owner limit reached")
)

// Options configures a Service. Zero values pick sensible defaults.
type Options struct {
	// ShardID is recorded in tenant_databases, so merchants can later be spread over servers.
	ShardID string // default "s1"
	// MaxOwned caps how many merchants one user can own. Every merchant is a database file,
	// so an open sign-up needs a limit. Zero means 5, negative means unlimited.
	MaxOwned int
	// DB is how the merchant files are opened while provisioning.
	DB db.Options
	// FS holds the migrations. Default is the embedded set.
	FS fs.FS
	// Logger receives cleanup warnings. Default is slog.Default().
	Logger *slog.Logger
	// Now is the clock. Default is time.Now.
	Now func() time.Time
}

// MerchantController creates and lists merchants.
type MerchantController struct {
	control  *dbx.DB
	dataDir  string
	shardID  string
	maxOwned int
	dbOpts   db.Options
	fsys     fs.FS
	log      *slog.Logger
	now      func() time.Time

	tplMu sync.Mutex

	// afterCopy runs after the merchant file is in place, before it is registered as ready.
	// Tests use it to simulate a failure at that point.
	afterCopy func() error
}

// New returns a Service that registers merchants in control and stores their databases below
// dataDir/tenants.
func New(control *dbx.DB, dataDir string, o Options) *MerchantController {
	if o.ShardID == "" {
		o.ShardID = "s1"
	}
	if o.MaxOwned == 0 {
		o.MaxOwned = 5
	}
	if o.FS == nil {
		o.FS = migrations.FS
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &MerchantController{
		control: control, dataDir: dataDir, shardID: o.ShardID, maxOwned: o.MaxOwned,
		dbOpts: o.DB, fsys: o.FS, log: o.Logger, now: o.Now,
	}
}

// Create registers a merchant owned by in.UserID and provisions its database.
//
// Steps, and what a failure at each one leaves behind:
//  1. Validate. Nothing is written.
//  2. Make sure the template database exists. Nothing is registered yet.
//  3. In one control-plane transaction insert the merchant, its owner membership and a
//     tenant_databases row in status provisioning. A taken slug or the owner limit stops here.
//  4. Copy the template to the merchant's file, write the merchant_profile row and check the
//     schema version.
//  5. Mark the tenant_databases row ready.
//
// A failure in step 4 or 5 removes the file and the control-plane rows again, so the slug can
// be used again. If even that cleanup fails the row stays behind and ReapStale removes it later.
func (s *MerchantController) Create(ctx context.Context, in Input) (*entities.Merchant, error) {
	in, err := in.normalize()
	if err != nil {
		return nil, apperr.Wrap(err, apperr.InvalidArgument, "Validate error")
	}
	tplPath, version, err := s.ensureTemplate(ctx)
	if err != nil {
		return nil, err
	}

	id, err := uuid.NewV7()
	if err != nil {
		logger.Error("Can't generate uuid", "err", err)
		return nil, apperr.Newf(apperr.Internal, "Can't generate uuid: %w", err)
	}

	rel := path.Join("tenants", id.String()+".db")
	now := s.now().Unix()

	if err := s.register(ctx, id.String(), rel, in, now); err != nil {
		logger.Error("merchant: register error", "id", id, "err", err)
		return nil, err
	}
	if err := s.materialize(ctx, tplPath, version, id.String(), rel, in, now); err != nil {
		s.abort(ctx, id.String(), rel, err)
		logger.Error("merchant: provision", "id", id, "err", err)
		return nil, err
	}
	return &entities.Merchant{ID: id.String(), Slug: in.Slug, Name: in.Name, SchemaVersion: version}, nil
}

func (s *MerchantController) register(ctx context.Context, id, rel string, in Input, now int64) (err error) {
	tx, err := s.control.DB().BeginTx(ctx, nil)
	if err != nil {
		return apperr.Newf(apperr.Internal, "merchant: begin: %w", err)
	}
	defer func() {
		if err != nil {
			logger.Error("register error", "err", err)
			_ = tx.Rollback()
		}
	}()

	if s.maxOwned > 0 {
		var owned int
		if err = tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM merchant_members WHERE user_id = ? AND role = 'owner'`, in.UserID).Scan(&owned); err != nil {
			return apperr.Newf(apperr.InvalidArgument, "merchant: count owned: %w", err)
		}
		if owned >= s.maxOwned {
			return ErrLimitReached
		}
	}

	var taken int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM merchants WHERE slug = ?`, in.Slug).Scan(&taken); err != nil {
		return apperr.Newf(apperr.Conflict, "merchant: check slug: %w", err)
	}
	if taken > 0 {
		return ErrSlugTaken
	}

	if _, err = tx.ExecContext(ctx,
		`INSERT INTO merchants (id, slug, name, status, created_by, created_at, updated_at)
		 VALUES (?, ?, ?, 'active', ?, ?, ?)`, id, in.Slug, in.Name, in.UserID, now, now); err != nil {
		if isUnique(err) { // lost a race with a concurrent request for the same slug
			return ErrSlugTaken
		}
		return apperr.Newf(apperr.Internal, "merchant: insert merchant: %w", err)
	}
	memberID, err := uuid.NewV7()
	if err != nil {
		return apperr.Newf(apperr.Internal, "Can't generate uuid: %w", err)
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO merchant_members (id, merchant_id, user_id, role, created_at)
		 VALUES (?, ?, ?, 'owner', ?)`, memberID, id, in.UserID, now); err != nil {
		return apperr.Newf(apperr.Internal, "merchant: insert owner: %w", err)
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO tenant_databases (merchant_id, shard_id, db_path, schema_version, status, created_at, updated_at)
		 VALUES (?, ?, ?, 0, 'provisioning', ?, ?)`, id, s.shardID, rel, now, now); err != nil {
		return apperr.Newf(apperr.Internal, "merchant: insert tenant database: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return apperr.Newf(apperr.Internal, "merchant: commit: %w", err)
	}
	return nil
}

func (s *MerchantController) materialize(
	ctx context.Context,
	tplPath string,
	version uint,
	id, rel string,
	in Input,
	now int64,
) error {
	dst := db.ResolvePath(s.dataDir, rel)
	if err := copyFile(tplPath, dst); err != nil {
		return apperr.Newf(apperr.Internal, "copy template: %w", err)
	}
	if s.afterCopy != nil {
		if err := s.afterCopy(); err != nil {
			return err
		}
	}

	if err := s.initProfile(ctx, dst, version, id, in, now); err != nil {
		return err
	}

	res, err := s.control.DB().ExecContext(ctx,
		`UPDATE tenant_databases
		 SET status = 'ready', schema_version = ?, last_migrated_at = ?, last_error = NULL, updated_at = ?
		 WHERE merchant_id = ? AND status = 'provisioning'`, int64(version), now, s.now().Unix(), id)
	if err != nil {
		return apperr.Newf(apperr.Internal, "mark ready: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return apperr.Newf(apperr.Internal, "mark ready: updated %d rows, want 1", n)
	}
	return nil
}

// initProfile writes the single merchant_profile row and confirms the copied database really is
// at the expected schema version.
func (s *MerchantController) initProfile(
	ctx context.Context,
	file string,
	version uint,
	id string,
	in Input,
	now int64,
) (err error) {
	opts := s.dbOpts
	opts.MustExist = true
	sqlDB, err := db.Open(ctx, file, opts)
	if err != nil {
		return apperr.Newf(apperr.Internal, "open merchant database: %w", err)
	}
	defer func() {
		if cerr := sqlDB.Close(); err == nil && cerr != nil {
			err = apperr.Newf(apperr.Internal, "close merchant database: %w", cerr)
		}
	}()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return apperr.New(apperr.Internal, err.Error())
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var got int64
	var dirty bool
	if err = tx.QueryRowContext(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&got, &dirty); err != nil {
		return apperr.Newf(apperr.Internal, "read schema version: %w", err)
	}
	if dirty || got != int64(version) {
		return apperr.Newf(apperr.Internal, "copied database is at version %d dirty=%v, want %d", got, dirty, version)
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO merchant_profile (id, merchant_id, name, currency, timezone, created_at, updated_at)
		 VALUES (1, ?, ?, ?, ?, ?, ?)`, id, in.Name, in.Currency, in.Timezone, now, now); err != nil {
		return apperr.Newf(apperr.Internal, "insert profile: %w", err)
	}
	return tx.Commit()
}

// abort undoes a failed provisioning on a best-effort basis. It ignores ctx cancellation, since
// the cleanup matters most when the request was cancelled.
func (s *MerchantController) abort(ctx context.Context, id, rel string, cause error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()

	removeDBFiles(db.ResolvePath(s.dataDir, rel))

	// ON DELETE CASCADE removes the owner membership and the tenant_databases row.
	if _, err := s.control.DB().ExecContext(ctx, `DELETE FROM merchants WHERE id = ?`, id); err != nil {
		logger.Error(
			"merchant: cleanup failed, leaving the row for ReapStale",
			"merchant_id",
			id,
			"error",
			err,
		)
		_, _ = s.control.DB().ExecContext(ctx,
			`UPDATE tenant_databases SET status = 'failed', last_error = ?, updated_at = ? WHERE merchant_id = ?`,
			cause.Error(), s.now().Unix(), id)
		return
	}
	s.log.Warn(
		"merchant: provisioning failed and was rolled back",
		"merchant_id",
		id,
		"error",
		cause,
	)
}

// Membership is one merchant a user belongs to.
type Membership struct {
	MerchantID string
	Slug       string
	Name       string
	Role       string
}

// ListForUser returns the active, ready merchants userID belongs to, oldest first.
func (s *MerchantController) ListForUser(ctx context.Context, userID string) ([]Membership, error) {
	rows, err := s.control.DB().QueryContext(ctx,
		`SELECT m.id, m.slug, m.name, mm.role
		 FROM merchant_members mm
		 JOIN merchants m ON m.id = mm.merchant_id
		 JOIN tenant_databases t ON t.merchant_id = m.id
		 WHERE mm.user_id = ? AND m.status = 'active' AND t.status IN ('ready', 'migrating')
		 ORDER BY m.created_at, m.id`, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, apperr.Newf(apperr.NotFound, "No merchant found for UserID: %s", userID)
		}

		return nil, apperr.Newf(apperr.Internal, "merchant: list: %w", err)
	}
	defer rows.Close()

	var out []Membership
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.MerchantID, &m.Slug, &m.Name, &m.Role); err != nil {
			return nil, apperr.Wrap(err, apperr.Internal, "Error scanning data")
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ReapStale removes merchants whose provisioning never finished: a crash between steps 3 and 5,
// or a failed cleanup. Only rows that never reached ready are touched, those have
// last_migrated_at unset. Call it from a scheduled job with an age well above the time a
// provisioning can take, for example 15 minutes.
func (s *MerchantController) ReapStale(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := s.now().Add(-olderThan).Unix()
	rows, err := s.control.DB().QueryContext(ctx,
		`SELECT merchant_id, db_path FROM tenant_databases
		 WHERE last_migrated_at IS NULL AND status IN ('provisioning', 'failed') AND updated_at < ?`, cutoff)
	if err != nil {
		return 0, apperr.Wrap(err, apperr.Internal, "Error when selecting failed provisioning merchant")
	}
	type stale struct{ id, rel string }
	var list []stale
	for rows.Next() {
		var st stale
		if err := rows.Scan(&st.id, &st.rel); err != nil {
			_ = rows.Close()
			return 0, apperr.Wrap(err, apperr.Internal, "Scan error")
		}
		list = append(list, st)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, apperr.Wrap(err, apperr.Internal, "rows Error")
	}
	_ = rows.Close()

	reaped := 0
	for _, st := range list {
		// The path comes from the database, never delete anything that is not below dataDir.
		if !filepath.IsLocal(filepath.FromSlash(st.rel)) || !strings.HasPrefix(st.rel, "tenants/") {
			logger.Error(
				"merchant: refusing to reap unexpected path",
				"merchant_id",
				st.id,
				"db_path",
				st.rel,
			)
			continue
		}
		removeDBFiles(db.ResolvePath(s.dataDir, st.rel))
		if _, err := s.control.DB().ExecContext(ctx, `DELETE FROM merchants WHERE id = ?`, st.id); err != nil {
			return reaped, apperr.Wrap(err, apperr.Internal, "Delete merchants error")
		}
		reaped++
	}
	return reaped, nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE")
}
