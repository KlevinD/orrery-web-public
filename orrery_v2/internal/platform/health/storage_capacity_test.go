package health

import "testing"

func TestStorageCapacityUsesServiceAccountAvailability(t *testing.T) {
	s := StorageCapacity{TotalBytes: 60 << 30, FreeBytes: 2 << 30, AvailableBytes: 20 << 20, ReservedBytes: (2 << 30) - (20 << 20)}
	if !s.Low() || !s.Critical() {
		t.Fatal("reserved free blocks counted as application capacity")
	}
	s.AvailableBytes = 4 << 30
	if s.Low() || s.Critical() {
		t.Fatal("capacity remained low after actual space recovery")
	}
	s.InodesTotal = 100
	s.InodesAvailable = 0
	if !s.Critical() {
		t.Fatal("inode exhaustion ignored")
	}
}
