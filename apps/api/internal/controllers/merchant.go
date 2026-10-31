// Package controllers holds the use cases: the business rules and the order in which the
// repositories are called. It knows nothing about SQL, files or HTTP. It talks to persistence
// through the small interfaces below, which package repo implements.
package controllers

import (
	"context"
	"time"

	"github.com/google/uuid"

	"encore.app/internal/apperr"
	"encore.app/internal/entities"
	"encore.app/internal/logger"
)

// MerchantRepository is the control-plane persistence a MerchantController needs.
type MerchantRepository interface {
	// Register writes the merchant, its owner and a provisioning tenant row atomically. It returns
	// entities.ErrSlugTaken or entities.ErrOwnerLimitReached (maxOwned <= 0 means unlimited).
	// It returns the times the database stamped on the merchant row.
	Register(ctx context.Context, reg entities.Registration, maxOwned int) (entities.Timestamps, error)
	MarkReady(ctx context.Context, merchantID string, version uint) error
	MarkFailed(ctx context.Context, merchantID, reason string) error
	// Remove deletes the merchant together with its owner and tenant rows.
	Remove(ctx context.Context, merchantID string) error
	ListForUser(ctx context.Context, userID string) ([]entities.Membership, error)
	// FindUnfinished lists tenant databases that never became ready and were last touched before cutoff.
	FindUnfinished(ctx context.Context, cutoff time.Time) ([]entities.TenantRef, error)
}

// TenantStore creates and removes merchant database files.
type TenantStore interface {
	// Provision creates the merchant's database with every tenant migration applied and writes its
	// profile. It returns the schema version the database is at.
	Provision(ctx context.Context, dbPath string, p entities.TenantProfile) (version uint, err error)
	// Discard removes the database file at dbPath, or refuses a path outside the tenants directory.
	Discard(dbPath string) error
}

// Options configures a MerchantController. Zero values pick sensible defaults.
type Options struct {
	// ShardID is recorded with every tenant database, so merchants can later be spread over servers.
	ShardID string // default "s1"
	// MaxOwned caps how many merchants one user can own. Every merchant is a database file, so an
	// open sign-up needs a limit. Zero means 5, negative means unlimited.
	MaxOwned int
	// Now is the clock. Default is time.Now.
	Now func() time.Time
}

const (
	defaultShardID  = "s1"
	defaultMaxOwned = 5
	cleanupTimeout  = 15 * time.Second
)

// MerchantController creates and lists merchants.
type MerchantController struct {
	merchants MerchantRepository
	tenants   TenantStore
	shardID   string
	maxOwned  int // <= 0: unlimited
	now       func() time.Time
}

// NewMerchantController wires the use cases to their persistence.
func NewMerchantController(m MerchantRepository, t TenantStore, o Options) *MerchantController {
	if o.ShardID == "" {
		o.ShardID = defaultShardID
	}
	switch {
	case o.MaxOwned == 0:
		o.MaxOwned = defaultMaxOwned
	case o.MaxOwned < 0:
		o.MaxOwned = 0
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &MerchantController{merchants: m, tenants: t, shardID: o.ShardID, maxOwned: o.MaxOwned, now: o.Now}
}

// Create registers a merchant owned by req.UserID and provisions its database.
//
// Steps, and what a failure at each one leaves behind:
//  1. Validate. Nothing is written.
//  2. Make sure the template database exists. Nothing is registered yet.
//  3. Register the merchant, its owner and a provisioning tenant row in one transaction. A taken
//     slug or the owner limit stops here.
//  4. Copy the template to the merchant's file and write the profile.
//  5. Mark the tenant ready.
//
// A failure in step 4 or 5 removes the file and the control-plane rows again, so the slug can be
// used again. If even that cleanup fails the row stays behind and ReapStale removes it later.
func (c *MerchantController) Create(ctx context.Context, req entities.MerchantRequest) (*entities.Merchant, error) {
	req, err := req.Normalize()
	if err != nil {
		return nil, err
	}
	merchantID, err := newID()
	if err != nil {
		return nil, err
	}
	memberID, err := newID()
	if err != nil {
		return nil, err
	}
	reg := entities.Registration{
		MerchantID: merchantID,
		MemberID:   memberID,
		OwnerID:    req.UserID,
		Slug:       req.Slug,
		Name:       req.Name,
		ShardID:    c.shardID,
		DBPath:     entities.TenantDBPath(merchantID),
	}
	stamps, err := c.merchants.Register(ctx, reg, c.maxOwned)
	if err != nil {
		return nil, err
	}

	profile := entities.TenantProfile{
		MerchantID: merchantID, Name: req.Name, Currency: req.Currency, Timezone: req.Timezone,
	}
	version, err := c.provision(ctx, reg, profile)
	if err != nil {
		c.rollback(ctx, reg, err)
		return nil, err
	}
	return &entities.Merchant{
		ID: merchantID, Slug: req.Slug, Name: req.Name, SchemaVersion: version, Timestamps: stamps,
	}, nil
}

func (c *MerchantController) provision(
	ctx context.Context,
	reg entities.Registration,
	p entities.TenantProfile,
) (uint, error) {
	version, err := c.tenants.Provision(ctx, reg.DBPath, p)
	if err != nil {
		return 0, err
	}
	return version, c.merchants.MarkReady(ctx, reg.MerchantID, version)
}

// rollback undoes a failed provisioning on a best-effort basis. It ignores ctx cancellation,
// since the cleanup matters most when the request was cancelled. It is the one place that logs a
// provisioning failure: the caller still gets the error, and logs it once at the edge.
func (c *MerchantController) rollback(ctx context.Context, reg entities.Registration, cause error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()

	// Both steps run even if the first fails, a leftover file is worse than a leftover row.
	discardErr := c.tenants.Discard(reg.DBPath)
	removeErr := c.merchants.Remove(ctx, reg.MerchantID)
	if discardErr == nil && removeErr == nil {
		logger.Warn("merchant provisioning failed and was rolled back",
			"merchant_id", reg.MerchantID, "cause", cause)
		return
	}
	logger.Error("merchant cleanup failed, leaving the row for ReapStale",
		"merchant_id", reg.MerchantID, "cause", cause, "discard_error", discardErr, "remove_error", removeErr)
	if removeErr != nil {
		if err := c.merchants.MarkFailed(ctx, reg.MerchantID, cause.Error()); err != nil {
			logger.Error("could not mark the tenant database as failed",
				"merchant_id", reg.MerchantID, "error", err)
		}
	}
}

// ListForUser returns the active, ready merchants userID belongs to, oldest first. A user without
// merchants gets an empty list.
func (c *MerchantController) ListForUser(ctx context.Context, userID string) ([]entities.Membership, error) {
	if userID == "" {
		return nil, apperr.New(apperr.Unauthenticated, "sign in first")
	}
	return c.merchants.ListForUser(ctx, userID)
}

// ReapStale removes merchants whose provisioning never finished: a crash between registering and
// marking ready, or a failed cleanup. Rows that were ready once are never touched. Call it from a
// scheduled job with an age well above the time a provisioning can take, for example 15 minutes.
// It returns how many merchants it removed.
func (c *MerchantController) ReapStale(ctx context.Context, olderThan time.Duration) (int, error) {
	stale, err := c.merchants.FindUnfinished(ctx, c.now().Add(-olderThan))
	if err != nil {
		return 0, err
	}
	reaped := 0
	for _, st := range stale {
		if err := c.tenants.Discard(st.DBPath); err != nil {
			logger.Error("refusing to reap merchant", "merchant_id", st.MerchantID, "db_path", st.DBPath, "error", err)
			continue
		}
		if err := c.merchants.Remove(ctx, st.MerchantID); err != nil {
			return reaped, err
		}
		reaped++
	}
	return reaped, nil
}

func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", apperr.Wrap(err, apperr.Internal, "internal error")
	}
	return id.String(), nil
}
