// Package tenantdb gives the API server its dbx handles: one for the control-plane database
// and one per merchant database.
//
// Encore only manages PostgreSQL through encore.dev/storage/sqldb. A Turso file is just a Go
// resource that you open yourself, so a service opens it in initService and closes it in
// Shutdown, see the example in the README that ships with this package.
package tenantdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/pocketbase/dbx"

	"encore.app/internal/db"
)

// ErrTenantUnavailable means the merchant has no database that is ready to serve.
var ErrTenantUnavailable = errors.New("tenantdb: merchant database is not available")

// Manager opens each database once and caches the handle for the life of the process. A Turso
// file should have a single owner process, so handles are shared, not opened per request.
//
// There is no eviction. Every cached tenant keeps its pooled connections open, so add an LRU
// before the number of merchants on one server grows into the hundreds.
type Manager struct {
	dataDir string
	opts    db.Options

	control *dbx.DB

	mu      sync.Mutex
	tenants map[string]*dbx.DB
	closed  bool
}

// New opens the control-plane database at <dataDir>/<controlFile>, creating it if missing.
func New(ctx context.Context, dataDir, controlFile string, opts db.Options) (*Manager, error) {
	opts.MustExist = false
	sqlDB, err := db.Open(ctx, db.ResolvePath(dataDir, controlFile), opts)
	if err != nil {
		return nil, fmt.Errorf("tenantdb: open control-plane database: %w", err)
	}
	return &Manager{
		dataDir: dataDir,
		opts:    opts,
		control: db.NewDBX(sqlDB),
		tenants: make(map[string]*dbx.DB),
	}, nil
}

// Control returns the control-plane handle.
func (m *Manager) Control() *dbx.DB { return m.control }

// Tenant returns the handle for a merchant database. The merchant must have a tenant_databases
// row with status ready.
//
// The lock is held while a missing database is opened, which briefly serializes first use of
// different merchants. That is a deliberate trade for simplicity.
func (m *Manager) Tenant(ctx context.Context, merchantID string) (*dbx.DB, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("tenantdb: manager is closed")
	}
	if h, ok := m.tenants[merchantID]; ok {
		return h, nil
	}

	var path, status string
	err := m.control.NewQuery("SELECT db_path, status FROM tenant_databases WHERE merchant_id = {:id}").
		WithContext(ctx).
		Bind(dbx.Params{"id": merchantID}).
		Row(&path, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTenantUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("tenantdb: look up merchant %s: %w", merchantID, err)
	}
	if status != "ready" {
		return nil, fmt.Errorf("%w: status is %s", ErrTenantUnavailable, status)
	}

	opts := m.opts
	opts.MustExist = true // never create a tenant database implicitly
	sqlDB, err := db.Open(ctx, db.ResolvePath(m.dataDir, path), opts)
	if err != nil {
		return nil, fmt.Errorf("tenantdb: open database of merchant %s: %w", merchantID, err)
	}
	h := db.NewDBX(sqlDB)
	m.tenants[merchantID] = h
	return h, nil
}

// Close closes every open database. It is safe to call more than once.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true

	errs := []error{m.control.Close()}
	for id, h := range m.tenants {
		if err := h.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close merchant %s: %w", id, err))
		}
	}
	m.tenants = nil
	return errors.Join(errs...)
}
