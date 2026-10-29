package errors_test

import (
	"errors"
	"testing"

	platformerrors "orrery_v2/internal/platform/errors"
)

func TestErrorCarriesStableCodeRetryableFieldAndMetadata(t *testing.T) {
	err := platformerrors.New(platformerrors.CodeDependencyUnavailable, "redis unavailable").
		WithField("liveness").
		WithRetryable(true).
		WithMetadata("backend", "redis")

	var coded *platformerrors.Error
	if !errors.As(err, &coded) {
		t.Fatalf("error does not unwrap to platform error")
	}
	if coded.Code != platformerrors.CodeDependencyUnavailable || !coded.Retryable || coded.Field != "liveness" || coded.Metadata["backend"] != "redis" {
		t.Fatalf("coded error = %+v", coded)
	}
}

func TestIsCodeMatchesWrappedErrors(t *testing.T) {
	err := platformerrors.Wrap(platformerrors.CodeNotFound, "artifact", errors.New("missing"))

	if !platformerrors.IsCode(err, platformerrors.CodeNotFound) {
		t.Fatalf("IsCode returned false for not_found")
	}
	if platformerrors.IsCode(err, platformerrors.CodeConflict) {
		t.Fatalf("IsCode returned true for conflict")
	}
}
