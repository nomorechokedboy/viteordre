// Package httperr is the delivery-layer edge: it translates application errors
// (internal/apperr) into Encore's errs package, which is what Encore turns into
// the HTTP status and JSON body. It is the only place that imports both.
//
// Call ToEncore at the boundary of every endpoint:
//
//	func (s *Service) Create(ctx context.Context, p *CreateParams) (*Merchant, error) {
//		m, err := s.uc.Create(ctx, p.toInput())
//		if err != nil {
//			return nil, httperr.ToEncore(err)
//		}
//		return toDTO(m), nil
//	}
package httperr

import (
	"context"
	"errors"

	"encore.dev/beta/errs"

	"encore.app/internal/apperr"
	"encore.app/internal/validate"
)

// ValidationDetails is sent to the client under "details" for InvalidArgument
// errors that carry per-field problems.
type ValidationDetails struct {
	Fields []apperr.FieldError `json:"fields"`
}

// ErrDetails implements errs.ErrDetails.
func (ValidationDetails) ErrDetails() {}

// ToEncore converts err for the wire. nil stays nil; an *apperr.Error is mapped
// by kind; an error that is already an *errs.Error (for example from the auth
// handler) is passed through.
// Anything that is not an *apperr.Error becomes a generic "internal error":
// its text is kept as the cause for logs and traces but is never sent to the client.
func ToEncore(err error) error {
	if err == nil {
		return nil
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		b := errs.B().Code(code(ae.Kind)).Msg(ae.Message)
		if ae.Err != nil {
			b = b.Cause(ae.Err)
		}
		if len(ae.Fields) > 0 {
			b = b.Details(ValidationDetails{Fields: ae.Fields})
		}
		return b.Err()
	}

	var ee *errs.Error
	if errors.As(err, &ee) {
		return err
	}

	switch {
	case errors.Is(err, context.Canceled):
		return errs.B().Code(errs.Canceled).Msg("request canceled").Cause(err).Err()
	case errors.Is(err, context.DeadlineExceeded):
		return errs.B().Code(errs.DeadlineExceeded).Msg("request timed out").Cause(err).Err()
	}
	return errs.B().Code(errs.Internal).Msg("internal error").Cause(err).Err()
}

// Validate runs struct-tag validation and returns the result in wire form. Use
// it as the body of a request type's Validate method, which Encore calls
// before the handler:
//
//	func (p *CreateParams) Validate() error { return httperr.Validate(p) }
func Validate(s any) error {
	return ToEncore(validate.Struct(s))
}

func code(k apperr.Kind) errs.ErrCode {
	switch k {
	case apperr.InvalidArgument:
		return errs.InvalidArgument
	case apperr.NotFound:
		return errs.NotFound
	case apperr.Conflict:
		return errs.AlreadyExists
	case apperr.Unauthenticated:
		return errs.Unauthenticated
	case apperr.PermissionDenied:
		return errs.PermissionDenied
	case apperr.FailedPrecondition:
		return errs.FailedPrecondition
	case apperr.Unavailable:
		return errs.Unavailable
	default:
		return errs.Internal
	}
}
