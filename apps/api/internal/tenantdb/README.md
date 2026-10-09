# Using dbx with Encore

Encore only manages PostgreSQL (`sqldb`). A Turso file is a plain Go resource: open it in your
service's `initService`, close it in `Shutdown`. Encore calls both.

```go
package orders

import (
	"context"
	"os"

	"encore.app/internal/db"
	"encore.app/internal/tenantdb"
)

//encore:service
type Service struct {
	dbs *tenantdb.Manager
}

func initService() (*Service, error) {
	m, err := tenantdb.New(context.Background(),
		envOr("DB_DATA_DIR", "./data"), envOr("DB_CONTROL_FILE", "control.db"),
		db.Options{MaxOpenConns: 8})
	if err != nil {
		return nil, err
	}
	return &Service{dbs: m}, nil
}

func (s *Service) Shutdown(force context.Context) { _ = s.dbs.Close() }

//encore:api public method=GET path=/merchants/:merchantID/tables
func (s *Service) ListTables(ctx context.Context, merchantID string) (*TablesResp, error) {
	h, err := s.dbs.Tenant(ctx, merchantID) // *dbx.DB for that merchant
	...
}

func envOr(k, d string) string { if v := os.Getenv(k); v != "" { return v }; return d }
```

Every Encore service in one process shares the same data directory, so only one service should
own the Manager; others call it through an Encore API.
