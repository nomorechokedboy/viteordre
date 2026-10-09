// Package apperr is the application's error vocabulary.
//
// It has no dependencies (in particular, no Encore), so the domain and use-case
// layers can return typed errors and be tested with plain `go test`. Only the
// delivery layer (see internal/httperr) translates them into Encore's errs
// package, which is what ends up on the wire.
//
// Rules of thumb:
//   - Domain / use case: return *Error (or wrap one with %w). Message is safe
//     to show to a client; never put SQL, paths or internals in it.
//   - Repository: translate driver errors (sql.ErrNoRows, unique violations)
//     into NotFound / Conflict, keeping the driver error as the cause.
//   - Anything that is not an *Error is treated as Internal at the edge, and
//     its text is never sent to the client.
package apperr

import (
	"errors"
	"fmt"
	"strings"
)

// Kind classifies an error. The zero value is Internal on purpose: an error
// that nobody classified must never be reported as the client's fault.
type Kind uint8

const (
	Internal Kind = iota
	InvalidArgument
	NotFound
	Conflict
	Unauthenticated
	PermissionDenied
	FailedPrecondition
	Unavailable
)

func (k Kind) String() string {
	switch k {
	case InvalidArgument:
		return "invalid_argument"
	case NotFound:
		return "not_found"
	case Conflict:
		return "conflict"
	case Unauthenticated:
		return "unauthenticated"
	case PermissionDenied:
		return "permission_denied"
	case FailedPrecondition:
		return "failed_precondition"
	case Unavailable:
		return "unavailable"
	default:
		return "internal"
	}
}

// FieldError describes one invalid input field.
type FieldError struct {
	// Field is the client-facing name (the JSON/query name, dotted for nested
	// values, e.g. "address.city").
	Field string `json:"field"`
	// Code is a short machine-readable reason: "required", "min", "slug"...
	Code string `json:"code"`
	// Message is a human-readable explanation.
	Message string `json:"message"`
}

// Error is the application error.
type Error struct {
	Kind Kind
	// Message is safe to return to clients.
	Message string
	// Fields is only set for InvalidArgument errors.
	Fields []FieldError
	// Err is the underlying cause. It is logged, never sent to clients.
	Err error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Kind.String())
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if len(e.Fields) > 0 {
		b.WriteString(" [")
		for i, f := range e.Fields {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(f.Field)
			b.WriteString(" ")
			b.WriteString(f.Message)
		}
		b.WriteString("]")
	}
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.Err }

// New creates an error of the given kind. Package-level errors made with New
// work as sentinels: errors.Is(err, ErrSlugTaken) matches wrapped copies.
func New(kind Kind, msg string) *Error { return &Error{Kind: kind, Message: msg} }

// Newf is New with formatting.
func Newf(kind Kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// Wrap attaches a client-safe message and a kind to a cause. Wrap(nil) is nil,
// so it can be used directly in a return statement.
func Wrap(err error, kind Kind, msg string) error {
	if err == nil {
		return nil
	}
	return &Error{Kind: kind, Message: msg, Err: err}
}

// KindOf returns the kind of the first *Error in err's chain, or Internal if
// there is none.
func KindOf(err error) Kind {
	var ae *Error
	if errors.As(err, &ae) {
		return ae.Kind
	}
	return Internal
}

// IsKind reports whether err's chain contains an *Error of the given kind.
func IsKind(err error, kind Kind) bool {
	var ae *Error
	return errors.As(err, &ae) && ae.Kind == kind
}

// FieldsOf returns the field errors of the first *Error in the chain.
func FieldsOf(err error) []FieldError {
	var ae *Error
	if errors.As(err, &ae) {
		return ae.Fields
	}
	return nil
}

// FieldErrors collects every problem found in one input, so the client can fix
// them all in one round trip instead of one per request.
//
//	var fe apperr.FieldErrors
//	if name == "" { fe.Add("name", "required", "is required") }
//	if !validSlug(slug) { fe.Add("slug", "slug", "must be lowercase letters, digits and dashes") }
//	return fe.Err()
type FieldErrors []FieldError

func (f *FieldErrors) Add(field, code, message string) {
	*f = append(*f, FieldError{Field: field, Code: code, Message: message})
}

// Err returns nil when nothing was collected, otherwise an InvalidArgument
// error carrying all fields.
func (f FieldErrors) Err() error {
	if len(f) == 0 {
		return nil
	}
	return &Error{Kind: InvalidArgument, Message: "invalid request", Fields: append([]FieldError(nil), f...)}
}
