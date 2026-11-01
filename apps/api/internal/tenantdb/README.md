# Using dbx with Encore

Encore only manages PostgreSQL (`sqldb`). A Turso file is a plain Go resource: get the shared
Manager in your service's `initService`, release it in `Shutdown`. Encore calls both.

```go
package orders

import (
	"context"
	"os"
	"time"

	"encore.app/internal/db"
	"encore.app/internal/tenantdb"
)

//encore:service
type Service struct {
	dbs *tenantdb.Manager
}

func initService() (*Service, error) {
	m, err := tenantdb.Default(context.Background(), tenantdb.Config{
		DataDir:     envOr("DB_DATA_DIR", "./data"),
		ControlFile: envOr("DB_CONTROL_FILE", "control.db"),
		Options:     db.Options{BusyTimeout: 10 * time.Second, MaxOpenConns: 8},
	})
	if err != nil {
		return nil, err
	}
	return &Service{dbs: m}, nil
}

func (s *Service) Shutdown(force context.Context) { _ = s.dbs.Release() }

//encore:api public method=GET path=/merchants/:merchantID/tables
func (s *Service) ListTables(ctx context.Context, merchantID string) (*TablesResp, error) {
	h, err := s.dbs.Tenant(ctx, merchantID) // *dbx.DB for that merchant
	...
}

func envOr(k, d string) string { if v := os.Getenv(k); v != "" { return v }; return d }
```

Every Encore service of one server runs in one process, and a Turso file allows one owner per
process. So all services get the same Manager from `tenantdb.Default`, each passing an equal
`Config` (a different one is an error), and each calls `Release` once. The files close when the last
service has released it. Do not call `tenantdb.New` in a service, it would open `control.db` a
second time.

`Tenant` opens merchants whose database is `ready` or `migrating`, the states `ListForUser` shows.
