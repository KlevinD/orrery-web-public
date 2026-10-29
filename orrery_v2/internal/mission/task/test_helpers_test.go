package task

import "testing"

func assertValidationError(t *testing.T, errs []ValidationError, code ValidationCode, field string) {
	t.Helper()
	for _, err := range errs {
		if err.Code == code && err.Field == field {
			return
		}
	}
	t.Fatalf("expected validation error %q on %q, got %#v", code, field, errs)
}
