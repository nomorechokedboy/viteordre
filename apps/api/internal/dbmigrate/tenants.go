package dbmigrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// TenantRef is one row of the control-plane tenant_databases table, joined with its merchant.
type TenantRef struct {
	MerchantID    string
	Slug          string
	DBPath        string
	Status        string
	SchemaVersion int
}

// Label is a short name for logs and tables.
func (t TenantRef) Label() string { return t.Slug }

// ListTenants returns the tenants to operate on.
//
// With selectors, each one is matched against merchant id or slug and must exist, any status
// is allowed because the CLI is also the repair tool. With selectors empty and all true it
// returns every tenant that has a usable database: ready, failed, or interrupted mid-migration.
// Tenants still provisioning or archived are skipped.
func ListTenants(ctx context.Context, control *sql.DB, selectors []string, all bool) ([]TenantRef, error) {
	const base = `SELECT t.merchant_id, m.slug, t.db_path, t.status, t.schema_version
		FROM tenant_databases t JOIN merchants m ON m.id = t.merchant_id`

	var out []TenantRef
	scan := func(rows *sql.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var t TenantRef
			if err := rows.Scan(&t.MerchantID, &t.Slug, &t.DBPath, &t.Status, &t.SchemaVersion); err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	}

	if len(selectors) > 0 {
		seen := map[string]bool{}
		for _, sel := range selectors {
			sel = strings.TrimSpace(sel)
			if sel == "" || seen[sel] {
				continue
			}
			seen[sel] = true
			rows, err := control.QueryContext(ctx, base+" WHERE m.id = ? OR m.slug = ?", sel, sel)
			if err != nil {
				return nil, err
			}
			before := len(out)
			if err := scan(rows); err != nil {
				return nil, err
			}
			if len(out) == before {
				return nil, fmt.Errorf("no tenant database registered for merchant %q", sel)
			}
		}
		return out, nil
	}

	if !all {
		return nil, fmt.Errorf("no tenants selected")
	}
	rows, err := control.QueryContext(ctx, base+
		" WHERE t.status IN ('ready', 'failed', 'migrating') ORDER BY m.slug")
	if err != nil {
		return nil, err
	}
	if err := scan(rows); err != nil {
		return nil, err
	}
	return out, nil
}

// MarkMigrating flags a tenant as being migrated, so a crashed run is visible as a stuck
// 'migrating' row. It never touches tenants that are provisioning or archived.
func MarkMigrating(ctx context.Context, control *sql.DB, merchantID string) error {
	_, err := control.ExecContext(ctx, `UPDATE tenant_databases
		SET status = 'migrating', last_error = NULL, updated_at = ?
		WHERE merchant_id = ? AND status IN ('ready', 'failed', 'migrating')`,
		time.Now().Unix(), merchantID)
	return err
}

// MarkFailed records that a tenant database could not be opened or prepared. The recorded
// schema version is left untouched because it is unknown.
func MarkFailed(ctx context.Context, control *sql.DB, merchantID string, opErr error) error {
	_, err := control.ExecContext(ctx, `UPDATE tenant_databases
		SET status = CASE WHEN status IN ('ready', 'failed', 'migrating') THEN 'failed' ELSE status END,
		    last_error = ?,
		    updated_at = ?
		WHERE merchant_id = ?`,
		opErr.Error(), time.Now().Unix(), merchantID)
	return err
}

// MarkResult records the outcome of an operation on a tenant database. version is the
// version actually found in the tenant database afterwards, or 0 if none is applied. A
// non-nil opErr or a dirty database marks the tenant failed. The status only changes for
// tenants that are ready, failed or migrating.
func MarkResult(ctx context.Context, control *sql.DB, merchantID string, version int, dirty bool, opErr error) error {
	now := time.Now().Unix()
	status, lastErr := "ready", sql.NullString{}
	switch {
	case opErr != nil:
		status, lastErr = "failed", sql.NullString{String: opErr.Error(), Valid: true}
	case dirty:
		status, lastErr = "failed", sql.NullString{String: "database is dirty, fix the failed migration then run force", Valid: true}
	}
	_, err := control.ExecContext(ctx, `UPDATE tenant_databases
		SET schema_version = ?,
		    status = CASE WHEN status IN ('ready', 'failed', 'migrating') THEN ? ELSE status END,
		    last_error = ?,
		    last_migrated_at = ?,
		    updated_at = ?
		WHERE merchant_id = ?`,
		version, status, lastErr, now, now, merchantID)
	return err
}
