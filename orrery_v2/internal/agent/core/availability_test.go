package core

import "testing"

func TestAvailabilityStatusExplicitlyTreatsUnknownAsUnavailable(t *testing.T) {
	if AvailabilityUnknown.IsAvailable() {
		t.Fatalf("unknown availability must not be silently available")
	}
	if !AvailabilityAvailable.IsAvailable() {
		t.Fatalf("available status should be available")
	}
}
