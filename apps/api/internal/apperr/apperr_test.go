package apperr_test

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"encore.app/internal/apperr"
)

var ErrSlugTaken = apperr.New(apperr.Conflict, "slug already taken")

func TestKindOf(t *testing.T) {
	cause := sql.ErrNoRows
	cases := []struct {
		name string
		err  error
		want apperr.Kind
	}{
		{"nil", nil, apperr.Internal},
		{"plain error is internal", errors.New("boom"), apperr.Internal},
		{"direct", apperr.New(apperr.NotFound, "x"), apperr.NotFound},
		{"wrapped with %w", fmt.Errorf("repo: %w", apperr.New(apperr.Conflict, "x")), apperr.Conflict},
		{"Wrap keeps kind", apperr.Wrap(cause, apperr.NotFound, "merchant not found"), apperr.NotFound},
		{"zero Kind is internal", &apperr.Error{Message: "unclassified"}, apperr.Internal},
	}
	for _, c := range cases {
		if got := apperr.KindOf(c.err); got != c.want {
			t.Errorf("%s: KindOf = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWrap(t *testing.T) {
	if apperr.Wrap(nil, apperr.NotFound, "x") != nil {
		t.Fatal("Wrap(nil) must be nil")
	}
	err := apperr.Wrap(sql.ErrNoRows, apperr.NotFound, "merchant not found")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Error("cause must stay reachable with errors.Is")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Message != "merchant not found" {
		t.Errorf("errors.As failed: %v", ae)
	}
}

func TestSentinel(t *testing.T) {
	wrapped := fmt.Errorf("create merchant: %w", ErrSlugTaken)
	if !errors.Is(wrapped, ErrSlugTaken) {
		t.Error("sentinel must match through %w")
	}
	if !apperr.IsKind(wrapped, apperr.Conflict) || apperr.IsKind(wrapped, apperr.NotFound) {
		t.Error("IsKind wrong")
	}
}

func TestFieldErrors(t *testing.T) {
	var fe apperr.FieldErrors
	if fe.Err() != nil {
		t.Fatal("empty FieldErrors must give a nil error (not a typed nil)")
	}
	fe.Add("name", "required", "is required")
	fe.Add("slug", "slug", "is invalid")
	err := fe.Err()
	if apperr.KindOf(err) != apperr.InvalidArgument {
		t.Fatalf("kind = %v", apperr.KindOf(err))
	}
	got := apperr.FieldsOf(fmt.Errorf("wrap: %w", err))
	if len(got) != 2 || got[0].Field != "name" || got[1].Code != "slug" {
		t.Errorf("fields = %+v", got)
	}
	// Returned error must not alias the collector's backing array.
	fe.Add("x", "y", "z")
	if len(apperr.FieldsOf(err)) != 2 {
		t.Error("error aliases collector slice")
	}
}

func TestErrorString(t *testing.T) {
	err := apperr.Wrap(errors.New("UNIQUE constraint failed"), apperr.Conflict, "slug already taken")
	want := "conflict: slug already taken: UNIQUE constraint failed"
	if err.Error() != want {
		t.Errorf("got %q want %q", err.Error(), want)
	}
}
