package idgen_test

import (
	"errors"
	"strings"
	"testing"

	"orrery_v2/internal/platform/idgen"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func TestDeterministicGeneratorReturnsStableSequence(t *testing.T) {
	gen := idgen.NewDeterministic("event")

	if got := gen.NewID(); got != "event-000001" {
		t.Fatalf("first id = %q", got)
	}
	if got := gen.NewID(); got != "event-000002" {
		t.Fatalf("second id = %q", got)
	}
}

func TestRandomGeneratorReturnsErrorInsteadOfPanickingWhenEntropyFails(t *testing.T) {
	gen := idgen.NewRandomWithReader("event", failingReader{})

	id, err := gen.NewIDContext()
	if err == nil {
		t.Fatalf("NewIDContext returned nil error")
	}
	if id != "" {
		t.Fatalf("id = %q, want empty on failure", id)
	}
	if !strings.Contains(err.Error(), "generate random id") || !strings.Contains(err.Error(), "entropy unavailable") {
		t.Fatalf("error = %v", err)
	}
}

func TestRandomGeneratorDoesNotExposePanicOnlyInterface(t *testing.T) {
	gen := idgen.NewRandom("event")

	if _, ok := any(gen).(idgen.Generator); ok {
		t.Fatalf("random generator implements panic-only Generator interface; production callers must use NewIDContext")
	}
	if _, ok := any(gen).(idgen.ErrorGenerator); !ok {
		t.Fatalf("random generator does not implement error-returning ErrorGenerator interface")
	}
}
