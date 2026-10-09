# Errors and validation

Three small packages, one direction of dependency (inner → outer is forbidden):

| Package | Layer | Imports Encore? |
|---|---|---|
| `internal/apperr` | shared vocabulary: `Kind`, `Error`, `FieldErrors` | no |
| `internal/validate` | transport: go-playground/validator wrapper | no |
| `internal/httperr` | delivery edge: `apperr` → Encore `errs` | yes |

## Who does what

```
handler (Encore service)   Validate() -> httperr.Validate(p)   shape: required, length, format
   |                       return nil, httperr.ToEncore(err)    the only place errors become HTTP
usecase                    returns apperr errors                rules needing I/O (slug taken, ownership)
domain                     constructors return apperr errors    invariants that always hold
repo                       maps driver errors                   ErrNoRows -> NotFound, UNIQUE -> Conflict
```

Write each rule once. If the domain defines what a slug is, expose it to the
validator instead of repeating the regexp in a struct tag:

```go
// service init
_ = validate.Register("slug", domain.ValidSlug, "may contain lowercase letters, digits and single dashes only")

type CreateParams struct {
	Name string `json:"name" validate:"required,min=2,max=80"`
	Slug string `json:"slug" validate:"required,slug"`
}
func (p *CreateParams) Validate() error { return httperr.Validate(p) }
```

The domain constructor still checks it (`NewSlug`), because other callers than
the HTTP handler will exist (CLI, jobs, tests).

## Patterns

```go
// sentinel, in the domain/usecase package
var ErrSlugTaken = apperr.New(apperr.Conflict, "slug already taken")

// repo
if isUniqueViolation(err) { return ErrSlugTaken }          // or apperr.Wrap(err, apperr.Conflict, "...")
if errors.Is(err, sql.ErrNoRows) {
	return apperr.Wrap(err, apperr.NotFound, "merchant not found")
}
return apperr.Wrap(err, apperr.Internal, "load merchant")  // cause is logged, never sent

// domain: report every problem at once
var fe apperr.FieldErrors
if name == "" { fe.Add("name", "required", "is required") }
return fe.Err() // nil when empty

// handler
m, err := s.uc.Create(ctx, in)
if err != nil { return nil, httperr.ToEncore(err) }
```

Wire format for field errors (Encore puts it under `details`):

```json
{"code":"invalid_argument","message":"invalid request","details":{"fields":[{"field":"name","code":"min","message":"must be at least 2 characters"}]}}
```

Anything that is not an `*apperr.Error` becomes `internal` with the message
"internal error"; the original text only goes to logs/traces.
