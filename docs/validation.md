# Validation guide

Aarv validates bound request structs with `validate` tags. Validators are
compiled and cached per request type when routes are registered.

Validation runs after binding and defaults, before the handler. Failures return
`*aarv.ValidationErrors`, which the default error handler serializes as HTTP
422.

## Basic rules

```go
type CreateUserReq struct {
    Name  string `json:"name" validate:"required,min=2,max=100"`
    Email string `json:"email" validate:"required,email"`
    Age   int    `json:"age" validate:"gte=0,lte=150"`
    Role  string `json:"role" default:"user" validate:"oneof=admin user moderator"`
}
```

Rules are comma-separated. Rules with values use `=`.

```go
validate:"required,min=2,max=100"
validate:"oneof=admin user editor"
validate:"datetime=2006-01-02"
validate:"regex=^[a-z0-9_]+$"
```

## Built-in rules

| Rule | Meaning |
|---|---|
| `required` | value must be non-zero |
| `omitempty` | skip remaining field rules when value is zero |
| `min=n` | string/slice/map length or numeric value must be at least n |
| `max=n` | string/slice/map length or numeric value must be at most n |
| `gte=n` | numeric/length value must be greater than or equal to n |
| `lte=n` | numeric/length value must be less than or equal to n |
| `gt=n` | numeric/length value must be greater than n |
| `lt=n` | numeric/length value must be less than n |
| `len=n` | string/slice/map/array length must equal n |
| `oneof=a b c` | value must match one option |
| `email` | string must look like an email address |
| `url` | string must parse as a request URI |
| `uuid` | string must be a UUID |
| `alpha` | string must contain only letters |
| `numeric` | string must contain only digits |
| `alphanum` | string must contain only letters or digits |
| `ip` | string must parse as an IP address |
| `ipv4` | string must parse as IPv4 |
| `ipv6` | string must parse as IPv6 |
| `cidr` | string must parse as CIDR |
| `json` | string must contain valid JSON |
| `datetime=layout` | string must parse with the Go time layout |
| `regex=pattern` | string must match the regular expression |
| `contains=x` | string must contain x |
| `startswith=x` | string must start with x |
| `endswith=x` | string must end with x |
| `excludes=x` | string must not contain x |
| `unique` | slice values must be unique |
| `dive` | validate each slice or map element with following rules |

For non-string type-specific rules like `email` or `uuid`, non-string fields
are treated as valid. Keep tags aligned with field types.

## Optional fields

Use `omitempty` for partial updates.

```go
type UpdateUserReq struct {
    Name  string `json:"name" validate:"omitempty,min=2,max=100"`
    Email string `json:"email" validate:"omitempty,email"`
}
```

`omitempty` skips all rules on the field when the value is zero.

## Nested structs

Nested structs are validated recursively. Error fields are reported with a
dotted path.

```go
type Address struct {
    City  string `json:"city" validate:"required"`
    State string `json:"state" validate:"len=2"`
}

type CreateReq struct {
    Name    string  `json:"name" validate:"required"`
    Address Address `json:"address"`
}
```

## Slices and maps

Use `dive` to apply rules to each element.

```go
type InviteReq struct {
    Emails []string `json:"emails" validate:"required,min=1,dive,email"`
    Roles  []string `json:"roles" validate:"omitempty,dive,oneof=admin user viewer"`
}
```

For slices or maps of structs, `dive` also runs nested struct validation.

## Self validation

Implement `SelfValidator` when a type owns all of its validation.

```go
func (r CreateWindowReq) Validate() []aarv.ValidationError {
    if r.End.Before(r.Start) {
        return []aarv.ValidationError{{
            Field:   "end",
            Tag:     "after_start",
            Message: "end must be after start",
        }}
    }
    return nil
}
```

`Validate` runs before field validation. If it returns errors, those errors
are returned directly.

## Struct-level validation

Use `StructLevelValidator` for cross-field checks that should run after field
rules.

```go
func (r CreateWindowReq) ValidateStruct() []aarv.ValidationError {
    if !r.End.After(r.Start) {
        return []aarv.ValidationError{{
            Field:   "end",
            Tag:     "after_start",
            Message: "end must be after start",
        }}
    }
    return nil
}
```

You can also register validators for a type:

```go
aarv.RegisterStructValidation(reflect.TypeOf(CreateWindowReq{}), func(v any) []aarv.ValidationError {
    req := v.(*CreateWindowReq)
    if !req.End.After(req.Start) {
        return []aarv.ValidationError{{Field: "end", Tag: "after_start", Message: "end must be after start"}}
    }
    return nil
})
```

Register custom validators at process startup before requests are served.

## Custom field rules

Register a rule and use it in tags.

```go
aarv.RegisterRule("slug", func(field reflect.Value, _ string) bool {
    return field.Kind() != reflect.String || slugRE.MatchString(field.String())
})

type CreateArticleReq struct {
    Slug string `json:"slug" validate:"required,slug"`
}
```

Custom rules receive the field value and optional parameter string.

## Custom messages

Override generated messages per rule:

```go
aarv.SetValidationMessageTemplate("required", func(field, _ string) string {
    return field + " is required"
})
```

Passing nil removes the override.

## Error shape

Validation failures are returned as:

```go
type ValidationErrors struct {
    Errors []aarv.ValidationError `json:"details"`
}
```

Each `ValidationError` contains `Field`, `Tag`, optional `Param`, optional
`Value`, and `Message`. With `plugins/problem`, validation failures become
RFC 7807 responses with the failures under the `errors` extension.

## Production guidance

- Treat validation messages as public API text.
- Use `omitempty` for PATCH-style request structs.
- Put cross-field business constraints in struct-level validation.
- Keep expensive validation out of tags; call services from handlers instead.
- Register custom rules and messages during startup, not during requests.
- Prefer stable custom `Tag` names so clients can branch on them.
