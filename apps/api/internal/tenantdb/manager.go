// Package tenantdb gives the API server its dbx handles: one for the control-plane database
// and one per merchant database.
//
// Encore only manages PostgreSQL through encore.dev/storage/sqldb. A Turso file is just a Go
// resource that you open yourself, so a service opens it in initService and closes it in
// Shutdown, see the example in the README that ships with this package.
//
// All Encore services of one server run in one process, and a Turso file allows a single owner
// per process, so they must share one Manager. Use Default, not New, in a service's initService.
package tenantdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/pocketbase/dbx"

	"encore.app/internal/db"
	"encore.app/internal/entities"
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

	shared bool // made by Default, closed by the last Release
}

// Config says where the databases live. Every service passes the same one to Default.
type Config struct {
	DataDir     string
	ControlFile string
	Options     db.Options
}

// shared is the process-wide Manager behind Default.
var shared struct {
	mu   sync.Mutex
	m    *Manager
	cfg  Config
	refs int
}

// Default returns the process-wide Manager, opening it on the first call. Each successful call must
// be paired with one Release, normally in the service's Shutdown, and the databases close when the
// last service has released it.
//
// Every caller must pass an equal Config. A different one is an error rather than a second Manager
// or a silent winner, because two Managers would open control.db twice in one process.
func Default(ctx context.Context, cfg Config) (*Manager, error) {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if shared.m != nil {
		if !reflect.DeepEqual(cfg, shared.cfg) {
			return nil, errors.New("tenantdb: Default called with a different Config than the one already in use")
		}
		shared.refs++
		return shared.m, nil
	}
	m, err := New(ctx, cfg.DataDir, cfg.ControlFile, cfg.Options)
	if err != nil {
		return nil, err
	}
	m.shared = true
	shared.m, shared.cfg, shared.refs = m, cfg, 1
	return m, nil
}

// Release gives back a Manager obtained from Default and closes it with the last release. On a
// Manager made by New it is the same as Close.
func (m *Manager) Release() error {
	if !m.shared {
		return m.Close()
	}
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if shared.m != m || shared.refs == 0 {
		return nil // already released
	}
	shared.refs--
	if shared.refs > 0 {
		return nil
	}
	shared.m = nil
	return m.Close()
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
// row with status ready or migrating, the same states that ListForUser shows, so a merchant that is
// listed can always be opened.
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
	if status != string(entities.TenantReady) && status != string(entities.TenantMigrating) {
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
