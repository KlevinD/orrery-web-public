package daemon

import "testing"

func TestLimiterRejectsWhenCapacityIsFull(t *testing.T) {
	limiter := NewLimiter(1)
	if !limiter.TryAcquire() {
		t.Fatalf("first acquire failed")
	}
	if limiter.TryAcquire() {
		t.Fatalf("second acquire should be rejected")
	}
	limiter.Release()
	if !limiter.TryAcquire() {
		t.Fatalf("acquire after release failed")
	}
}

func TestKeyedLimiterAllowsDifferentQueuesAndBlocksSameQueue(t *testing.T) {
	limiter := NewKeyedLimiter(1)
	if !limiter.TryAcquire("discussion:a") {
		t.Fatalf("first queue acquire failed")
	}
	if limiter.TryAcquire("discussion:a") {
		t.Fatalf("same queue should be blocked while active")
	}
	if !limiter.TryAcquire("discussion:b") {
		t.Fatalf("different queue should be allowed to run in parallel")
	}
	limiter.Release("discussion:a")
	if !limiter.TryAcquire("discussion:a") {
		t.Fatalf("same queue should acquire after release")
	}
}

func TestKeyedLimiterUsesDefaultKeyForBlankQueue(t *testing.T) {
	limiter := NewKeyedLimiter(1)
	if !limiter.TryAcquire("") {
		t.Fatalf("blank queue acquire failed")
	}
	if limiter.TryAcquire(" ") {
		t.Fatalf("blank queue aliases should share the same lane")
	}
	limiter.Release("")
	if !limiter.TryAcquire("default") {
		t.Fatalf("default queue should acquire after blank release")
	}
}
