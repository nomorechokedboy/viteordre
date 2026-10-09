// Package validate is the transport-level validation layer: it checks the
// shape of a request (required, length, format, range) with go-playground/validator
// struct tags and reports every violation as an apperr.InvalidArgument error.
//
// It deliberately knows nothing about business rules. Invariants of the domain
// (what a valid slug is, whether a price may be negative) live in the domain
// package; expose them here once with Register so a tag and the domain
// constructor can never disagree.
package validate

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"

	"encore.app/internal/apperr"
)

var (
	v        = newValidator()
	messages = map[string]func(validator.FieldError) string{}
)

func newValidator() *validator.Validate {
	val := validator.New(validator.WithRequiredStructEnabled())
	// Report the name the client actually sent: json, then query, then header.
	val.RegisterTagNameFunc(func(f reflect.StructField) string {
		for _, key := range []string{"json", "query", "header"} {
			name := strings.SplitN(f.Tag.Get(key), ",", 2)[0]
			if name == "-" {
				return ""
			}
			if name != "" {
				return name
			}
		}
		return f.Name
	})
	return val
}

// Register adds a custom tag backed by a plain predicate, e.g. the domain's own
// slug check. msg is the message shown when it fails.
//
//	validate.Register("slug", func(s string) bool { return domain.ValidSlug(s) },
//		"may contain lowercase letters, digits and dashes only")
//
// Call it from an init function or at service start; it is not safe to call
// concurrently with Struct.
func Register(tag string, fn func(string) bool, msg string) error {
	err := v.RegisterValidation(tag, func(fl validator.FieldLevel) bool {
		f := fl.Field()
		if f.Kind() != reflect.String {
			return false
		}
		return fn(f.String())
	})
	if err != nil {
		return err
	}
	messages[tag] = func(validator.FieldError) string { return msg }
	return nil
}

// Struct validates s (a struct or pointer to struct). It returns nil, or an
// *apperr.Error of kind InvalidArgument listing every violated field.
func Struct(s any) error {
	err := v.Struct(s)
	if err == nil {
		return nil
	}
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		// Programmer error (nil, non-struct, bad tag): not the client's fault.
		return apperr.Wrap(err, apperr.Internal, "internal error")
	}
	var out apperr.FieldErrors
	for _, fe := range verrs {
		out.Add(fieldName(fe), fe.Tag(), message(fe))
	}
	return out.Err()
}

// fieldName turns "CreateParams.address.city" into "address.city".
func fieldName(fe validator.FieldError) string {
	ns := fe.Namespace()
	if i := strings.Index(ns, "."); i >= 0 {
		return ns[i+1:]
	}
	return fe.Field()
}

func message(fe validator.FieldError) string {
	if fn, ok := messages[fe.Tag()]; ok {
		return fn(fe)
	}
	p := fe.Param()
	isText := fe.Kind() == reflect.String
	isList := fe.Kind() == reflect.Slice || fe.Kind() == reflect.Map || fe.Kind() == reflect.Array
	switch fe.Tag() {
	case "required":
		return "is required"
	case "required_with", "required_without", "required_if", "required_unless":
		return "is required"
	case "email":
		return "must be a valid email address"
	case "url", "http_url", "uri":
		return "must be a valid URL"
	case "uuid", "uuid4", "uuid_rfc4122":
		return "must be a valid UUID"
	case "alphanum":
		return "may contain letters and digits only"
	case "alpha":
		return "may contain letters only"
	case "numeric", "number":
		return "must be numeric"
	case "oneof":
		return "must be one of: " + strings.ReplaceAll(p, " ", ", ")
	case "eq":
		return "must equal " + p
	case "ne":
		return "must not equal " + p
	case "len":
		return sized(isText, isList, "must be exactly %s", p)
	case "min", "gte":
		return sized(isText, isList, "must be at least %s", p)
	case "max", "lte":
		return sized(isText, isList, "must be at most %s", p)
	case "gt":
		return sized(isText, isList, "must be more than %s", p)
	case "lt":
		return sized(isText, isList, "must be less than %s", p)
	default:
		return "is invalid"
	}
}

// sized words a size rule: "at least 3 characters" for text, "at least 3 items"
// for collections, plain "at least 3" for numbers.
func sized(isText, isList bool, format, p string) string {
	msg := fmt.Sprintf(format, p)
	switch {
	case isText:
		return msg + " characters"
	case isList:
		return msg + " items"
	}
	return msg
}
