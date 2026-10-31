// Package merchants is the Encore service for creating and listing merchants.
//
// It is the delivery layer only: it checks the shape of the payload, hands a plain request to
// the controller and turns the result, or the error, into the wire format. Business rules live in
// internal/entities and internal/controllers, SQL in internal/repo.
//
// It needs an Encore auth handler (//encore:authhandler) that returns the signed-in user's id,
// because both endpoints are declared with `auth`.
package merchants

import (
	"context"
	"os"
	"sync"
	"time"

	"encore.dev/beta/auth"

	"encore.app/internal/apperr"
	"encore.app/internal/controllers"
	"encore.app/internal/db"
	"encore.app/internal/entities"
	"encore.app/internal/httperr"
	"encore.app/internal/logger"
	"encore.app/internal/repo"
	"encore.app/internal/tenantdb"
)

//encore:service
type Service struct {
	dbs       *tenantdb.Manager
	merchants *controllers.MerchantController

	stopReaper context.CancelFunc
	reaperDone sync.WaitGroup
}

func initService() (*Service, error) {
	dataDir := envOr("DB_DATA_DIR", "./data")
	opts := db.Options{BusyTimeout: 10 * time.Second, MaxOpenConns: 8}

	dbs, err := tenantdb.New(context.Background(), dataDir, envOr("DB_CONTROL_FILE", "control.db"), opts)
	if err != nil {
		return nil, err
	}
	s := &Service{
		dbs: dbs,
		merchants: controllers.NewMerchantController(
			repo.NewMerchantRepo(dbs.Control()),
			repo.NewTenantStore(dataDir, nil, opts),
			controllers.Options{ShardID: envOr("DB_SHARD_ID", "s1")},
		),
	}

	// Encore cron jobs do not run when you self-host, so sweep abandoned provisioning rows with a
	// plain ticker. Anything older than 30 minutes cannot still be in flight.
	ctx, cancel := context.WithCancel(context.Background())
	s.stopReaper = cancel
	s.reaperDone.Add(1)
	go func() {
		defer s.reaperDone.Done()
		s.reap(ctx)
		t := time.NewTicker(15 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.reap(ctx)
			}
		}
	}()
	return s, nil
}

func (s *Service) reap(ctx context.Context) {
	n, err := s.merchants.ReapStale(ctx, 30*time.Minute)
	if err != nil && ctx.Err() == nil {
		logger.ErrorContext(ctx, "merchants: reap failed", "error", err)
	}
	if n > 0 {
		logger.WarnContext(ctx, "merchants: removed abandoned provisioning rows", "count", n)
	}
}

// Shutdown is called by Encore on graceful shutdown.
func (s *Service) Shutdown(force context.Context) {
	s.stopReaper()
	s.reaperDone.Wait()
	_ = s.dbs.Close()
}

// CreateParams is the body of POST /merchants.
//
// The tags only bound the size of the input. What a valid name, slug, currency or timezone is
// lives in entities.MerchantRequest, so the rules are written once.
type CreateParams struct {
	Name     string `json:"name"               validate:"required,max=200"`
	Slug     string `json:"slug"               validate:"required,max=100"`
	Currency string `json:"currency,omitempty" validate:"max=10"`  // ISO 4217, default VND
	Timezone string `json:"timezone,omitempty" validate:"max=100"` // IANA name, default Asia/Ho_Chi_Minh
}

// Validate is called by Encore before the handler runs.
func (p *CreateParams) Validate() error { return httperr.Validate(p) }

// Merchant is a merchant as returned by the API.
type Merchant struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
	// CreatedAt and UpdatedAt are set by the database, in UTC (RFC 3339 on the wire).
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Create makes a merchant owned by the signed-in user, with its own database.
//
//encore:api auth method=POST path=/merchants
func (s *Service) Create(ctx context.Context, p *CreateParams) (*Merchant, error) {
	uid, err := currentUser()
	if err != nil {
		return nil, err
	}
	m, err := s.merchants.Create(ctx, entities.MerchantRequest{
		UserID: uid, Name: p.Name, Slug: p.Slug, Currency: p.Currency, Timezone: p.Timezone,
	})
	if err != nil {
		return nil, httperr.ToEncore(err)
	}
	return &Merchant{
		ID: m.ID, Slug: m.Slug, Name: m.Name, Role: string(entities.RoleOwner),
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}, nil
}

// ListResponse is the body of GET /merchants.
type ListResponse struct {
	Merchants []Merchant `json:"merchants"`
}

// List returns the merchants the signed-in user belongs to.
//
//encore:api auth method=GET path=/merchants
func (s *Service) List(ctx context.Context) (*ListResponse, error) {
	uid, err := currentUser()
	if err != nil {
		return nil, err
	}
	ms, err := s.merchants.ListForUser(ctx, uid)
	if err != nil {
		return nil, httperr.ToEncore(err)
	}
	out := &ListResponse{Merchants: make([]Merchant, 0, len(ms))}
	for _, m := range ms {
		out.Merchants = append(out.Merchants, Merchant{
			ID: m.MerchantID, Slug: m.Slug, Name: m.Name, Role: string(m.Role),
			CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
		})
	}
	return out, nil
}

// currentUser returns the signed-in user's id, or an error ready to return from a handler.
func currentUser() (string, error) {
	uid, ok := auth.UserID()
	if !ok {
		return "", httperr.ToEncore(apperr.New(apperr.Unauthenticated, "sign in first"))
	}
	return string(uid), nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
