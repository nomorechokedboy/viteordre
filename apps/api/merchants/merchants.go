// Package merchants is the Encore service for creating and listing merchants.
//
// It needs an Encore auth handler (//encore:authhandler) that returns the signed-in user's id,
// because both endpoints are declared with `auth`.
package merchants

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"encore.dev/beta/auth"
	"encore.dev/beta/errs"

	"encore.app/internal/db"
	"encore.app/internal/merchant"
	"encore.app/internal/tenantdb"
)

//encore:service
type Service struct {
	dbs      *tenantdb.Manager
	merchant *merchant.MerchantController

	stopReaper context.CancelFunc
	reaperDone sync.WaitGroup
}

func initService() (*Service, error) {
	dataDir := envOr("DB_DATA_DIR", "./data")
	opts := db.Options{BusyTimeout: 10 * time.Second, MaxOpenConns: 8}

	dbs, err := tenantdb.New(
		context.Background(),
		dataDir,
		envOr("DB_CONTROL_FILE", "control.db"),
		opts,
	)
	if err != nil {
		return nil, err
	}
	s := &Service{
		dbs: dbs,
		merchant: merchant.New(dbs.Control(), dataDir, merchant.Options{
			ShardID: envOr("DB_SHARD_ID", "s1"),
			DB:      opts,
		}),
	}

	// Encore cron jobs do not run when you self-host, so sweep abandoned provisioning rows
	// with a plain ticker. Anything older than 30 minutes cannot still be in flight.
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
	n, err := s.merchant.ReapStale(ctx, 30*time.Minute)
	if err != nil && ctx.Err() == nil {
		slog.Error("merchants: reap failed", "error", err)
	}
	if n > 0 {
		slog.Warn("merchants: removed abandoned provisioning rows", "count", n)
	}
}

// Shutdown is called by Encore on graceful shutdown.
func (s *Service) Shutdown(force context.Context) {
	s.stopReaper()
	s.reaperDone.Wait()
	_ = s.dbs.Close()
}

// CreateParams is the body of POST /merchants.
type CreateParams struct {
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Currency string `json:"currency,omitempty"` // ISO 4217, default VND
	Timezone string `json:"timezone,omitempty"` // IANA name, default Asia/Ho_Chi_Minh
}

// Merchant is a merchant as returned by the API.
type Merchant struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

// Create makes a merchant owned by the signed-in user, with its own database.
//
//encore:api auth method=POST path=/merchants
func (s *Service) Create(ctx context.Context, p *CreateParams) (*Merchant, error) {
	uid, ok := auth.UserID()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("sign in first").Err()
	}
	m, err := s.merchant.Create(ctx, merchant.Input{
		UserID: string(uid), Name: p.Name, Slug: p.Slug, Currency: p.Currency, Timezone: p.Timezone,
	})
	if err != nil {
		return nil, toAPIError(err)
	}
	return &Merchant{ID: m.ID, Slug: m.Slug, Name: m.Name, Role: "owner"}, nil
}

// ListResponse is the body of GET /merchants.
type ListResponse struct {
	Merchants []Merchant `json:"merchants"`
}

// List returns the merchants the signed-in user belongs to.
//
//encore:api auth method=GET path=/merchants
func (s *Service) List(ctx context.Context) (*ListResponse, error) {
	uid, ok := auth.UserID()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("sign in first").Err()
	}
	ms, err := s.merchant.ListForUser(ctx, string(uid))
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("could not list merchants").Cause(err).Err()
	}
	out := &ListResponse{Merchants: make([]Merchant, 0, len(ms))}
	for _, m := range ms {
		out.Merchants = append(
			out.Merchants,
			Merchant{ID: m.MerchantID, Slug: m.Slug, Name: m.Name, Role: m.Role},
		)
	}
	return out, nil
}

func toAPIError(err error) error {
	var ve *merchant.ValidationError
	switch {
	case errors.As(err, &ve):
		return errs.B().Code(errs.InvalidArgument).Msg(ve.Error()).Meta("field", ve.Field).Err()
	case errors.Is(err, merchant.ErrSlugTaken):
		return errs.B().
			Code(errs.AlreadyExists).
			Msg("that slug is already taken").
			Meta("field", "slug").
			Err()
	case errors.Is(err, merchant.ErrLimitReached):
		return errs.B().
			Code(errs.ResourceExhausted).
			Msg("you have reached the maximum number of shops").
			Err()
	}
	return errs.B().Code(errs.Internal).Msg("could not create the merchant").Cause(err).Err()
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
