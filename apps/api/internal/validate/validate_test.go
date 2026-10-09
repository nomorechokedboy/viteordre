package validate_test

import (
	"regexp"
	"testing"

	"encore.app/internal/apperr"
	"encore.app/internal/validate"
)

type address struct {
	City string `json:"city" validate:"required"`
}

type params struct {
	Name  string   `json:"name" validate:"required,min=3,max=10"`
	Email string   `json:"email" validate:"omitempty,email"`
	Kind  string   `json:"kind" validate:"required,oneof=dine_in takeaway"`
	Price int64    `json:"price_minor" validate:"gte=0"`
	Tags  []string `json:"tags" validate:"max=2"`
	Page  int      `query:"page" validate:"gte=1"`
	Addr  address  `json:"address"`
}

func fields(t *testing.T, err error) map[string]apperr.FieldError {
	t.Helper()
	if apperr.KindOf(err) != apperr.InvalidArgument {
		t.Fatalf("kind = %v (%v)", apperr.KindOf(err), err)
	}
	m := map[string]apperr.FieldError{}
	for _, f := range apperr.FieldsOf(err) {
		m[f.Field] = f
	}
	return m
}

func TestValid(t *testing.T) {
	p := params{Name: "Pho 24", Kind: "dine_in", Page: 1, Addr: address{City: "HCMC"}}
	if err := validate.Struct(&p); err != nil {
		t.Fatal(err)
	}
}

func TestReportsEveryFieldWithClientNames(t *testing.T) {
	p := params{
		Name: "ab", Email: "nope", Kind: "delivery", Price: -1,
		Tags: []string{"a", "b", "c"}, Page: 0,
	}
	m := fields(t, validate.Struct(p))
	want := map[string][2]string{
		"name":         {"min", "must be at least 3 characters"},
		"email":        {"email", "must be a valid email address"},
		"kind":         {"oneof", "must be one of: dine_in, takeaway"},
		"price_minor":  {"gte", "must be at least 0"},
		"tags":         {"max", "must be at most 2 items"},
		"page":         {"gte", "must be at least 1"},
		"address.city": {"required", "is required"},
	}
	if len(m) != len(want) {
		t.Errorf("got %d fields: %+v", len(m), m)
	}
	for field, w := range want {
		got, ok := m[field]
		if !ok {
			t.Errorf("missing %q", field)
			continue
		}
		if got.Code != w[0] || got.Message != w[1] {
			t.Errorf("%s = {%s, %q}, want {%s, %q}", field, got.Code, got.Message, w[0], w[1])
		}
	}
}

func TestRegisterCustomRule(t *testing.T) {
	re := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	if err := validate.Register("slug", re.MatchString, "may contain lowercase letters, digits and single dashes only"); err != nil {
		t.Fatal(err)
	}
	type req struct {
		Slug string `json:"slug" validate:"required,slug"`
	}
	if err := validate.Struct(req{Slug: "pho-24"}); err != nil {
		t.Fatal(err)
	}
	m := fields(t, validate.Struct(req{Slug: "Pho 24"}))
	if m["slug"].Code != "slug" || m["slug"].Message != "may contain lowercase letters, digits and single dashes only" {
		t.Errorf("got %+v", m["slug"])
	}
}

func TestProgrammerErrorsAreInternal(t *testing.T) {
	for _, in := range []any{nil, 42, "str"} {
		err := validate.Struct(in)
		if err == nil || apperr.KindOf(err) != apperr.Internal {
			t.Errorf("Struct(%v) = %v, want internal error", in, err)
		}
	}
}
