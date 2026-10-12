package entities

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	// Embed the timezone database so time.LoadLocation works in a minimal container image.
	_ "time/tzdata"

	"encore.app/internal/apperr"
)

const (
	MinSlugLen = 3
	MaxSlugLen = 40
	MaxNameLen = 80

	DefaultCurrency = "VND"
	DefaultTimezone = "Asia/Ho_Chi_Minh"
)

var (
	slugRe     = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

	// reservedSlugs are path segments the app or the platform may want for itself.
	reservedSlugs = map[string]bool{
		"admin": true, "api": true, "app": true, "assets": true, "auth": true, "billing": true,
		"dashboard": true, "docs": true, "healthz": true, "help": true, "login": true,
		"logout": true, "m": true, "merchants": true, "new": true, "settings": true,
		"signup": true, "static": true, "status": true, "support": true, "t": true,
		"webhooks": true, "www": true,
	}
)

// MerchantRequest is what a signed-in user supplies to create a merchant.
type MerchantRequest struct {
	UserID   string // required, the owner
	Name     string // required, shown to customers
	Slug     string // required, used in public URLs, lowercase letters, digits and hyphens
	Currency string // optional ISO 4217 code, default VND
	Timezone string // optional IANA name, default Asia/Ho_Chi_Minh
}

// Normalize trims and defaults the request, then checks every rule. It returns the cleaned
// request, or an InvalidArgument error that lists every field that is wrong, so a form can show
// all problems at once.
//
// These are the invariants of a merchant: they hold no matter who calls (the HTTP handler, a CLI
// or a job). The transport layer only checks the shape of the payload.
func (r MerchantRequest) Normalize() (MerchantRequest, error) {
	r.UserID = strings.TrimSpace(r.UserID)
	if r.UserID == "" {
		// Not something the client can fix by editing the form.
		return r, apperr.New(apperr.Unauthenticated, "sign in first")
	}

	var fe apperr.FieldErrors

	r.Name = strings.TrimSpace(r.Name)
	switch n := utf8.RuneCountInString(r.Name); {
	case n == 0:
		fe.Add("name", "required", "is required")
	case n > MaxNameLen:
		fe.Add("name", "max", "must be at most 80 characters")
	case strings.ContainsFunc(r.Name, unicode.IsControl):
		fe.Add("name", "invalid", "must not contain control characters")
	}

	r.Slug = strings.ToLower(strings.TrimSpace(r.Slug))
	switch {
	case r.Slug == "":
		fe.Add("slug", "required", "is required")
	case len(r.Slug) < MinSlugLen || len(r.Slug) > MaxSlugLen:
		fe.Add("slug", "length", "must be 3 to 40 characters")
	case !slugRe.MatchString(r.Slug):
		fe.Add("slug", "format",
			"may only contain lowercase letters, digits and single hyphens, and cannot start or end with a hyphen")
	case reservedSlugs[r.Slug]:
		fe.Add("slug", "reserved", "is reserved")
	}

	r.Currency = strings.ToUpper(strings.TrimSpace(r.Currency))
	if r.Currency == "" {
		r.Currency = DefaultCurrency
	}
	if !currencyRe.MatchString(r.Currency) {
		fe.Add("currency", "format", "must be a three-letter ISO 4217 code")
	}

	r.Timezone = strings.TrimSpace(r.Timezone)
	if r.Timezone == "" {
		r.Timezone = DefaultTimezone
	}
	if _, err := time.LoadLocation(r.Timezone); err != nil || r.Timezone == "Local" {
		fe.Add("timezone", "invalid", "is not a known IANA timezone")
	}

	return r, fe.Err()
}
