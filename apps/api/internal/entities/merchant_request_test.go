package entities_test

import (
	"strings"
	"testing"

	"encore.app/internal/apperr"
	"encore.app/internal/entities"
)

func fields(err error) map[string]string {
	out := map[string]string{}
	for _, f := range apperr.FieldsOf(err) {
		out[f.Field] = f.Code
	}
	return out
}

func TestNormalizeCleansAndDefaults(t *testing.T) {
	got, err := entities.MerchantRequest{
		UserID: " u1 ", Name: "  Phở Bà Tư ", Slug: " Pho-Ba-Tu ", Currency: "vnd",
	}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	want := entities.MerchantRequest{
		UserID: "u1", Name: "Phở Bà Tư", Slug: "pho-ba-tu", Currency: "VND", Timezone: "Asia/Ho_Chi_Minh",
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestNormalizeRejects(t *testing.T) {
	// with returns a valid request changed by f.
	with := func(f func(*entities.MerchantRequest)) entities.MerchantRequest {
		r := entities.MerchantRequest{UserID: "u", Name: "A", Slug: "abc"}
		f(&r)
		return r
	}

	tests := []struct {
		name  string
		in    entities.MerchantRequest
		field string
		code  string
	}{
		{"no name", with(func(r *entities.MerchantRequest) { r.Name = "  " }), "name", "required"},
		{"name too long", with(func(r *entities.MerchantRequest) { r.Name = strings.Repeat("x", 81) }), "name", "max"},
		{"name counts runes", with(func(r *entities.MerchantRequest) { r.Name = strings.Repeat("ế", 80) }), "", ""},
		{"control char", with(func(r *entities.MerchantRequest) { r.Name = "a\x00b" }), "name", "invalid"},
		{"no slug", with(func(r *entities.MerchantRequest) { r.Slug = "" }), "slug", "required"},
		{"short slug", with(func(r *entities.MerchantRequest) { r.Slug = "ab" }), "slug", "length"},
		{"long slug", with(func(r *entities.MerchantRequest) { r.Slug = strings.Repeat("a", 41) }), "slug", "length"},
		{"slug chars", with(func(r *entities.MerchantRequest) { r.Slug = "pho_ba" }), "slug", "format"},
		{"slug edge hyphen", with(func(r *entities.MerchantRequest) { r.Slug = "-pho" }), "slug", "format"},
		{"slug double hyphen", with(func(r *entities.MerchantRequest) { r.Slug = "pho--ba" }), "slug", "format"},
		{"slug reserved", with(func(r *entities.MerchantRequest) { r.Slug = "admin" }), "slug", "reserved"},
		{"currency", with(func(r *entities.MerchantRequest) { r.Currency = "dong" }), "currency", "format"},
		{"timezone", with(func(r *entities.MerchantRequest) { r.Timezone = "Mars/Base" }), "timezone", "invalid"},
		{"timezone local", with(func(r *entities.MerchantRequest) { r.Timezone = "Local" }), "timezone", "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.in.Normalize()
			if tt.field == "" {
				if err != nil {
					t.Fatalf("want no error, got %v", err)
				}
				return
			}
			if !apperr.IsKind(err, apperr.InvalidArgument) {
				t.Fatalf("want InvalidArgument, got %v", err)
			}
			if got := fields(err)[tt.field]; got != tt.code {
				t.Fatalf("field %q code = %q, want %q (all: %v)", tt.field, got, tt.code, fields(err))
			}
		})
	}
}

func TestNormalizeReportsEveryProblemAtOnce(t *testing.T) {
	_, err := entities.MerchantRequest{UserID: "u", Name: "", Slug: "A_", Currency: "x", Timezone: "Mars/Base"}.Normalize()
	got := fields(err)
	for _, f := range []string{"name", "slug", "currency", "timezone"} {
		if _, ok := got[f]; !ok {
			t.Errorf("missing a problem for %q: %v", f, got)
		}
	}
}

func TestNormalizeWithoutUserIsUnauthenticated(t *testing.T) {
	_, err := entities.MerchantRequest{Name: "A", Slug: "abc"}.Normalize()
	if !apperr.IsKind(err, apperr.Unauthenticated) {
		t.Fatalf("got %v, want Unauthenticated", err)
	}
}

func TestSentinels(t *testing.T) {
	if !apperr.IsKind(entities.ErrSlugTaken, apperr.Conflict) || fields(entities.ErrSlugTaken)["slug"] != "taken" {
		t.Errorf("ErrSlugTaken = %v", entities.ErrSlugTaken)
	}
	if !apperr.IsKind(entities.ErrOwnerLimitReached, apperr.FailedPrecondition) {
		t.Errorf("ErrOwnerLimitReached = %v", entities.ErrOwnerLimitReached)
	}
}

func TestTenantDBPath(t *testing.T) {
	if got := entities.TenantDBPath("abc"); got != "tenants/abc.db" {
		t.Fatalf("got %q", got)
	}
}
