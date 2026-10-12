package merchant

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	// Embed the timezone database so time.LoadLocation works in a minimal container image.
	_ "time/tzdata"
)

const (
	minSlug = 3
	maxSlug = 40
	maxName = 80

	defaultCurrency = "VND"
	defaultTimezone = "Asia/Ho_Chi_Minh"
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

// ValidationError describes input the caller can fix.
type ValidationError struct {
	Field string
	Msg   string
}

func (e *ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Msg) }

func invalid(field, format string, a ...any) error {
	return &ValidationError{Field: field, Msg: fmt.Sprintf(format, a...)}
}

// Input is what a signed-in user supplies to create a merchant.
type Input struct {
	UserID   string // required, the owner
	Name     string // required, shown to customers
	Slug     string // required, used in public URLs, lowercase letters, digits and hyphens
	Currency string // optional ISO 4217 code, default VND
	Timezone string // optional IANA name, default Asia/Ho_Chi_Minh
}

// normalize trims and defaults the input, then validates it.
func (in Input) normalize() (Input, error) {
	in.UserID = strings.TrimSpace(in.UserID)
	if in.UserID == "" {
		return in, invalid("user", "is required")
	}

	in.Name = strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(in.Name); n == 0 || n > maxName {
		return in, invalid("name", "must be 1 to %d characters", maxName)
	}
	for _, r := range in.Name {
		if unicode.IsControl(r) {
			return in, invalid("name", "must not contain control characters")
		}
	}

	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	switch {
	case len(in.Slug) < minSlug || len(in.Slug) > maxSlug:
		return in, invalid("slug", "must be %d to %d characters", minSlug, maxSlug)
	case !slugRe.MatchString(in.Slug):
		return in, invalid(
			"slug",
			"may only contain lowercase letters, digits and single hyphens, and cannot start or end with a hyphen",
		)
	case reservedSlugs[in.Slug]:
		return in, invalid("slug", "%q is reserved", in.Slug)
	}

	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Currency == "" {
		in.Currency = defaultCurrency
	}
	if !currencyRe.MatchString(in.Currency) {
		return in, invalid("currency", "must be a three-letter ISO 4217 code")
	}

	in.Timezone = strings.TrimSpace(in.Timezone)
	if in.Timezone == "" {
		in.Timezone = defaultTimezone
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil || in.Timezone == "Local" {
		return in, invalid("timezone", "%q is not a known IANA timezone", in.Timezone)
	}
	return in, nil
}
