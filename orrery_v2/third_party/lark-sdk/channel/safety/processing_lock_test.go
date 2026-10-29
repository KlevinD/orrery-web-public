package safety

import (
	"testing"
	"time"
)

func TestProcessingLock(t *testing.T) {
	pl := NewProcessingLock(50*time.Millisecond, 100*time.Millisecond)
	defer pl.Dispose()

	id := "test-id"

	// Acquire lock
	if !pl.Acquire(id) {
		t.Error("Expected to acquire lock")
	}

	// Try to acquire again
	if pl.Acquire(id) {
		t.Error("Expected to fail acquiring lock")
	}

	// Release lock
	pl.Release(id)

	// Acquire lock again
	if !pl.Acquire(id) {
		t.Error("Expected to acquire lock after release")
	}

	// Wait for TTL to expire
	time.Sleep(60 * time.Millisecond)

	// Acquire lock again
	if !pl.Acquire(id) {
		t.Error("Expected to acquire lock after TTL")
	}

	// Wait for sweep
	time.Sleep(150 * time.Millisecond)

	pl.mu.Lock()
	_, exists := pl.locks[id]
	pl.mu.Unlock()

	if exists {
		t.Error("Expected lock to be swept")
	}
}

func TestProcessingLockDisposeIsIdempotentAndRejectsNewClaims(t *testing.T) {
	pl := NewProcessingLock(time.Minute, time.Minute)
	pl.Dispose()
	pl.Dispose()
	if pl.Acquire("after-close") {
		t.Fatal("disposed processing lock accepted a new claim")
	}
}
