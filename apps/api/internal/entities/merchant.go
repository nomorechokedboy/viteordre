// Package entities holds the application's core types: what a merchant is, who belongs to it and
// which rules its input must satisfy. It depends on nothing but apperr, so every other layer can
// import it and it can be tested with plain `go test`.
package entities

import (
	"path"
	"time"

	"encore.app/internal/apperr"
)

// Role is what a user may do inside a merchant.
type Role string

// RoleOwner is the user who created the merchant.
const RoleOwner Role = "owner"

// TenantStatus is the lifecycle state of a merchant's database file.
type TenantStatus string

const (
	TenantProvisioning TenantStatus = "provisioning" // registered, file not ready yet
	TenantReady        TenantStatus = "ready"
	TenantMigrating    TenantStatus = "migrating"
	TenantFailed       TenantStatus = "failed" // cleanup after a failed provisioning did not finish
)

// Timestamps are the row times the database keeps itself (created_at, updated_at). Application
// code reads them and never sets them.
type Timestamps struct {
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Merchant is a shop that was created and is ready to serve.
type Merchant struct {
	ID            string
	Slug          string
	Name          string
	SchemaVersion uint
	Timestamps
}

// Membership is one merchant a user belongs to.
type Membership struct {
	MerchantID string
	Slug       string
	Name       string
	Role       Role
	Timestamps // of the merchant row
}

// Registration is everything the control plane records about a new merchant: the merchant row,
// its owner membership and its tenant database row, written together.
type Registration struct {
	MerchantID string
	MemberID   string // id of the owner's membership row
	OwnerID    string
	Slug       string
	Name       string
	ShardID    string
	DBPath     string // relative to the data directory, see TenantDBPath
}

// TenantProfile is the single merchant_profile row written into a new merchant database.
type TenantProfile struct {
	MerchantID string
	Name       string
	Currency   string
	Timezone   string
}

// TenantRef points at the database file of a merchant.
type TenantRef struct {
	MerchantID string
	DBPath     string
}

// TenantDBPath is where a merchant's database lives, relative to the data directory.
func TenantDBPath(merchantID string) string {
	return path.Join("tenants", merchantID+".db")
}

var (
	// ErrSlugTaken means another merchant already uses the slug. The message is shown to clients.
	ErrSlugTaken = &apperr.Error{
		Kind:    apperr.Conflict,
		Message: "that slug is already taken",
		Fields:  []apperr.FieldError{{Field: "slug", Code: "taken", Message: "is already taken"}},
	}
	// ErrOwnerLimitReached means the user already owns the maximum number of merchants.
	ErrOwnerLimitReached = apperr.New(
		apperr.FailedPrecondition,
		"you have reached the maximum number of shops",
	)
)
