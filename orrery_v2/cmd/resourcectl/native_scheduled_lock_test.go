package main

import "testing"

func TestNativeScheduledLockRejectsConcurrentCutoverAndAllowsReacquisition(t *testing.T) {
	base := t.TempDir()
	release, err := acquireNativeScheduledLock(base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := acquireNativeScheduledLock(base)
	if err == nil {
		second()
		release()
		t.Fatal("concurrent cutover acquired the same migration lock")
	}
	release()
	third, err := acquireNativeScheduledLock(base)
	if err != nil {
		t.Fatalf("released migration lock could not be acquired again: %v", err)
	}
	third()
}
