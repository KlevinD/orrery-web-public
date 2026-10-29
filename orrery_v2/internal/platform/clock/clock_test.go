package clock_test

import (
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
)

func TestFakeClockAdvancesDeterministically(t *testing.T) {
	start := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	c := clock.NewFake(start)

	c.Advance(90 * time.Second)

	if got := c.Now(); !got.Equal(start.Add(90 * time.Second)) {
		t.Fatalf("Now = %v, want %v", got, start.Add(90*time.Second))
	}
}
