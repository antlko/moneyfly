// Package apperr holds the typed error sentinels the domain returns and the
// transport layer maps to HTTP status codes.
//
// See docs/implementation-plan/00-conventions.md §5. Errors are never matched
// by string; callers use errors.Is.
package apperr

import (
	"errors"
	"fmt"
	"strings"
)

// The sentinels. Transport maps each to an HTTP status; callers match with
// errors.Is and never by string (conventions §5).
var (
	// ErrNotFound is returned when a row does not exist, or exists but belongs to
	// another user — the two are indistinguishable to the client on purpose.
	ErrNotFound = errors.New("not found")
	// ErrConflict is a uniqueness or state conflict.
	ErrConflict = errors.New("conflict")
	// ErrValidation is a bad request body or parameter.
	ErrValidation = errors.New("validation")
	// ErrUnauthorized means there is no valid session.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrForbidden means the session is valid but the action is not allowed.
	ErrForbidden = errors.New("forbidden")
	// ErrTooLarge means an upload exceeded the configured cap. It is separate
	// from ErrValidation because 413 is the honest answer: the request was
	// well-formed, it was simply too big (docs/implementation-plan/04-monefy-import.md §11).
	ErrTooLarge = errors.New("payload too large")
)

// FieldError is one field-level problem, rendered in a 422 response.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationError carries every field problem found, not just the first, so a
// client can fix a form in one round trip.
type ValidationError struct {
	Fields []FieldError
}

// Error implements error.
func (v *ValidationError) Error() string {
	if len(v.Fields) == 0 {
		return "validation failed"
	}
	parts := make([]string, 0, len(v.Fields))
	for _, f := range v.Fields {
		parts = append(parts, f.Field+": "+f.Message)
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// Unwrap makes errors.Is(err, ErrValidation) true for a *ValidationError, so
// transport needs one mapping rule rather than two.
func (v *ValidationError) Unwrap() error { return ErrValidation }

// Add records another field problem.
func (v *ValidationError) Add(field, format string, args ...any) {
	v.Fields = append(v.Fields, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
}

// Empty reports whether anything was recorded.
func (v *ValidationError) Empty() bool { return len(v.Fields) == 0 }

// OrNil returns nil when nothing was recorded, so callers can `return v.OrNil()`.
func (v *ValidationError) OrNil() error {
	if v.Empty() {
		return nil
	}
	return v
}

// Validation builds a ValidationError for a single field.
func Validation(field, format string, args ...any) *ValidationError {
	v := &ValidationError{}
	v.Add(field, format, args...)
	return v
}

// NotFoundf wraps ErrNotFound with context for the log; the client sees only the status.
func NotFoundf(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrNotFound)
}

// Conflictf wraps ErrConflict with context.
func Conflictf(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrConflict)
}

// Forbiddenf wraps ErrForbidden with context.
func Forbiddenf(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrForbidden)
}

// Unauthorizedf wraps ErrUnauthorized with context.
func Unauthorizedf(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrUnauthorized)
}

// TooLargef wraps ErrTooLarge with context.
func TooLargef(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrTooLarge)
}
