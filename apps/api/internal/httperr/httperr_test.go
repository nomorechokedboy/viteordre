package httperr_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"encore.dev/beta/errs"

	"encore.app/internal/apperr"
	"encore.app/internal/httperr"
)

func asErrs(t *testing.T, err error) *errs.Error {
	t.Helper()
	var ee *errs.Error
	if !errors.As(err, &ee) {
		t.Fatalf("not an *errs.Error: %T %v", err, err)
	}
	return ee
}

func TestKindMapping(t *testing.T) {
	cases := map[apperr.Kind]errs.ErrCode{
		apperr.InvalidArgument:    errs.InvalidArgument,
		apperr.NotFound:           errs.NotFound,
		apperr.Conflict:           errs.AlreadyExists,
		apperr.Unauthenticated:    errs.Unauthenticated,
		apperr.PermissionDenied:   errs.PermissionDenied,
		apperr.FailedPrecondition: errs.FailedPrecondition,
		apperr.Unavailable:        errs.Unavailable,
		apperr.Internal:           errs.Internal,
	}
	for k, want := range cases {
		ee := asErrs(t, httperr.ToEncore(apperr.New(k, "msg")))
		if ee.Code != want || ee.Message != "msg" {
			t.Errorf("%v -> {%v, %q}, want {%v, msg}", k, ee.Code, ee.Message, want)
		}
	}
}

func TestNil(t *testing.T) {
	if httperr.ToEncore(nil) != nil {
		t.Fatal("nil must stay nil")
	}
}

func TestCauseIsKeptButNotSent(t *testing.T) {
	err := apperr.Wrap(errors.New("UNIQUE constraint failed: merchants.slug"), apperr.Conflict, "slug already taken")
	ee := asErrs(t, httperr.ToEncore(fmt.Errorf("usecase: %w", err)))
	if ee.Message != "slug already taken" || strings.Contains(ee.Message, "UNIQUE") {
		t.Errorf("client message leaked internals: %q", ee.Message)
	}
	if !strings.Contains(ee.Error(), "UNIQUE constraint failed") {
		t.Errorf("cause must be kept for logs: %v", ee)
	}
}

func TestUnknownErrorsAreInternalAndOpaque(t *testing.T) {
	ee := asErrs(t, httperr.ToEncore(errors.New("dial tcp 10.0.0.5:6379: refused")))
	if ee.Code != errs.Internal || ee.Message != "internal error" {
		t.Errorf("got {%v, %q}", ee.Code, ee.Message)
	}
	ee = asErrs(t, httperr.ToEncore(sql.ErrNoRows))
	if ee.Code != errs.Internal {
		t.Errorf("a raw driver error must not leak as not-found: %v", ee.Code)
	}
}

func TestContextErrors(t *testing.T) {
	if c := asErrs(t, httperr.ToEncore(fmt.Errorf("q: %w", context.Canceled))).Code; c != errs.Canceled {
		t.Errorf("canceled -> %v", c)
	}
	if c := asErrs(t, httperr.ToEncore(context.DeadlineExceeded)).Code; c != errs.DeadlineExceeded {
		t.Errorf("deadline -> %v", c)
	}
}

func TestPassThroughEncoreErrors(t *testing.T) {
	orig := errs.B().Code(errs.Unauthenticated).Msg("invalid or expired token").Err()
	if got := httperr.ToEncore(orig); got != orig {
		t.Error("an *errs.Error must pass through unchanged")
	}
}

func TestAppErrWinsOverWrappedEncoreErr(t *testing.T) {
	inner := errs.B().Code(errs.Internal).Msg("inner").Err()
	ee := asErrs(t, httperr.ToEncore(apperr.Wrap(inner, apperr.NotFound, "merchant not found")))
	if ee.Code != errs.NotFound {
		t.Errorf("got %v", ee.Code)
	}
}

type createParams struct {
	Name string `json:"name" validate:"required,min=3"`
	Slug string `json:"slug" validate:"required"`
}

func TestValidateDetails(t *testing.T) {
	err := httperr.Validate(&createParams{Name: "x"})
	ee := asErrs(t, err)
	if ee.Code != errs.InvalidArgument {
		t.Fatalf("code = %v", ee.Code)
	}
	d, ok := ee.Details.(httperr.ValidationDetails)
	if !ok || len(d.Fields) != 2 {
		t.Fatalf("details = %#v", ee.Details)
	}
	b, _ := json.Marshal(d)
	want := `{"fields":[{"field":"name","code":"min","message":"must be at least 3 characters"},{"field":"slug","code":"required","message":"is required"}]}`
	if string(b) != want {
		t.Errorf("wire form:\n got %s\nwant %s", b, want)
	}
	if httperr.Validate(&createParams{Name: "abc", Slug: "s"}) != nil {
		t.Error("valid input must give nil")
	}
}
