package apperr

import (
	"errors"
	"testing"
)

func TestValidationError_IsErrValidation(t *testing.T) {
	v := Validation("amount_minor", "must be greater than zero")
	if !errors.Is(v, ErrValidation) {
		t.Fatal("a *ValidationError must satisfy errors.Is(err, ErrValidation)")
	}
	var target *ValidationError
	if !errors.As(error(v), &target) || len(target.Fields) != 1 {
		t.Fatal("errors.As must recover the field detail")
	}
}

func TestValidationError_CollectsEveryField(t *testing.T) {
	v := &ValidationError{}
	if v.OrNil() != nil {
		t.Fatal("empty ValidationError must return nil from OrNil")
	}
	v.Add("amount_minor", "must be greater than zero")
	v.Add("occurred_on", "must be a date in YYYY-MM-DD form")
	if len(v.Fields) != 2 {
		t.Fatalf("want 2 fields, got %d", len(v.Fields))
	}
	if v.OrNil() == nil {
		t.Fatal("non-empty ValidationError must return itself")
	}
}

func TestWrappers(t *testing.T) {
	for _, tc := range []struct {
		err      error
		sentinel error
	}{
		{NotFoundf("category %d", 4), ErrNotFound},
		{Conflictf("name %q taken", "Food"), ErrConflict},
		{Forbiddenf("password change required"), ErrForbidden},
		{Unauthorizedf("no session"), ErrUnauthorized},
	} {
		if !errors.Is(tc.err, tc.sentinel) {
			t.Fatalf("%v must wrap %v", tc.err, tc.sentinel)
		}
	}
}
