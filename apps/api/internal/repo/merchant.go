package repo

import (
	"context"
	"time"

	"github.com/pocketbase/dbx"

	"encore.app/internal/entities"
)

// MerchantRepo stores merchants in the control-plane database.
type MerchantRepo struct {
	db *dbx.DB
}

// NewMerchantRepo returns a repository on the control-plane handle. The caller owns the handle.
func NewMerchantRepo(control *dbx.DB) *MerchantRepo { return &MerchantRepo{db: control} }

// Register writes the merchant, its owner membership and a tenant database row in status
// provisioning, in one transaction.
//
// It returns entities.ErrOwnerLimitReached when maxOwned is positive and the owner already has that
// many merchants, and entities.ErrSlugTaken when the slug is in use, including when a concurrent
// request won the race for it. Nothing is written in either case.
func (r *MerchantRepo) Register(ctx context.Context, reg entities.Registration, maxOwned int) error {
	at := reg.At.Unix()
	err := r.db.TransactionalContext(ctx, nil, func(tx *dbx.Tx) error {
		if maxOwned > 0 {
			var owned int
			if err := tx.NewQuery(
				`SELECT COUNT(*) FROM merchant_members WHERE user_id = {:user} AND role = {:role}`).
				WithContext(ctx).
				Bind(dbx.Params{"user": reg.OwnerID, "role": string(entities.RoleOwner)}).
				Row(&owned); err != nil {
				return internal("count owned merchants", err)
			}
			if owned >= maxOwned {
				return entities.ErrOwnerLimitReached
			}
		}

		_, err := tx.NewQuery(
			`INSERT INTO merchants (id, slug, name, status, created_by, created_at, updated_at)
			 VALUES ({:id}, {:slug}, {:name}, 'active', {:user}, {:at}, {:at})`).
			WithContext(ctx).
			Bind(dbx.Params{
				"id": reg.MerchantID, "slug": reg.Slug, "name": reg.Name, "user": reg.OwnerID, "at": at,
			}).Execute()
		if isUniqueViolation(err) {
			return entities.ErrSlugTaken
		}
		if err != nil {
			return internal("insert merchant", err)
		}

		if _, err := tx.NewQuery(
			`INSERT INTO merchant_members (id, merchant_id, user_id, role, created_at)
			 VALUES ({:id}, {:merchant}, {:user}, {:role}, {:at})`).
			WithContext(ctx).
			Bind(dbx.Params{
				"id": reg.MemberID, "merchant": reg.MerchantID, "user": reg.OwnerID,
				"role": string(entities.RoleOwner), "at": at,
			}).Execute(); err != nil {
			return internal("insert owner membership", err)
		}

		if _, err := tx.NewQuery(
			`INSERT INTO tenant_databases (merchant_id, shard_id, db_path, schema_version, status, created_at, updated_at)
			 VALUES ({:merchant}, {:shard}, {:path}, 0, {:status}, {:at}, {:at})`).
			WithContext(ctx).
			Bind(dbx.Params{
				"merchant": reg.MerchantID, "shard": reg.ShardID, "path": reg.DBPath,
				"status": string(entities.TenantProvisioning), "at": at,
			}).Execute(); err != nil {
			return internal("insert tenant database", err)
		}
		return nil
	})
	return internal("register merchant", err) // begin and commit failures; classified errors pass through
}

// MarkReady flips a provisioning tenant database to ready at schema version.
func (r *MerchantRepo) MarkReady(ctx context.Context, merchantID string, version uint, at time.Time) error {
	res, err := r.db.NewQuery(
		`UPDATE tenant_databases
		 SET status = {:ready}, schema_version = {:version}, last_migrated_at = {:at}, last_error = NULL, updated_at = {:at}
		 WHERE merchant_id = {:id} AND status = {:provisioning}`).
		WithContext(ctx).
		Bind(dbx.Params{
			"ready": string(entities.TenantReady), "provisioning": string(entities.TenantProvisioning),
			"version": int64(version), "at": at.Unix(), "id": merchantID,
		}).Execute()
	if err != nil {
		return internal("mark tenant ready", err)
	}
	if n, err := res.RowsAffected(); err == nil && n != 1 {
		return internal("mark tenant ready", errUnexpectedRows(n))
	}
	return nil
}

// MarkFailed records why a tenant database could not be cleaned up, so ReapStale can retry.
func (r *MerchantRepo) MarkFailed(ctx context.Context, merchantID, reason string, at time.Time) error {
	_, err := r.db.NewQuery(
		`UPDATE tenant_databases SET status = {:failed}, last_error = {:reason}, updated_at = {:at}
		 WHERE merchant_id = {:id}`).
		WithContext(ctx).
		Bind(dbx.Params{
			"failed": string(entities.TenantFailed), "reason": reason, "at": at.Unix(), "id": merchantID,
		}).Execute()
	return internal("mark tenant failed", err)
}

// Remove deletes a merchant. ON DELETE CASCADE removes the owner membership and the tenant
// database row with it.
func (r *MerchantRepo) Remove(ctx context.Context, merchantID string) error {
	_, err := r.db.NewQuery(`DELETE FROM merchants WHERE id = {:id}`).
		WithContext(ctx).Bind(dbx.Params{"id": merchantID}).Execute()
	return internal("remove merchant", err)
}

// ListForUser returns the active merchants userID belongs to whose database can serve requests,
// oldest first. A user without merchants gets an empty list, not an error.
func (r *MerchantRepo) ListForUser(ctx context.Context, userID string) ([]entities.Membership, error) {
	var rows []struct {
		ID   string `db:"id"`
		Slug string `db:"slug"`
		Name string `db:"name"`
		Role string `db:"role"`
	}
	err := r.db.NewQuery(
		`SELECT m.id, m.slug, m.name, mm.role
		 FROM merchant_members mm
		 JOIN merchants m ON m.id = mm.merchant_id
		 JOIN tenant_databases t ON t.merchant_id = m.id
		 WHERE mm.user_id = {:user} AND m.status = 'active' AND t.status IN ({:ready}, {:migrating})
		 ORDER BY m.created_at, m.id`).
		WithContext(ctx).
		Bind(dbx.Params{
			"user": userID, "ready": string(entities.TenantReady), "migrating": string(entities.TenantMigrating),
		}).All(&rows)
	if err != nil {
		return nil, internal("list merchants of user", err)
	}
	out := make([]entities.Membership, 0, len(rows))
	for _, row := range rows {
		out = append(out, entities.Membership{
			MerchantID: row.ID, Slug: row.Slug, Name: row.Name, Role: entities.Role(row.Role),
		})
	}
	return out, nil
}

// FindUnfinished returns the tenant databases that never reached ready and were last touched
// before cutoff: a crash between registering and marking ready, or a cleanup that failed.
// Rows that were ready once have last_migrated_at set and are never returned.
func (r *MerchantRepo) FindUnfinished(ctx context.Context, cutoff time.Time) ([]entities.TenantRef, error) {
	var rows []struct {
		MerchantID string `db:"merchant_id"`
		DBPath     string `db:"db_path"`
	}
	err := r.db.NewQuery(
		`SELECT merchant_id, db_path FROM tenant_databases
		 WHERE last_migrated_at IS NULL AND status IN ({:provisioning}, {:failed}) AND updated_at < {:cutoff}`).
		WithContext(ctx).
		Bind(dbx.Params{
			"provisioning": string(entities.TenantProvisioning), "failed": string(entities.TenantFailed),
			"cutoff": cutoff.Unix(),
		}).All(&rows)
	if err != nil {
		return nil, internal("find unfinished tenant databases", err)
	}
	out := make([]entities.TenantRef, 0, len(rows))
	for _, row := range rows {
		out = append(out, entities.TenantRef{MerchantID: row.MerchantID, DBPath: row.DBPath})
	}
	return out, nil
}
