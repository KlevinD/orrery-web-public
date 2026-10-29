package health

import (
	"golang.org/x/sys/unix"
	"path/filepath"
	"time"
)

func ReadStorageCapacity(path string) (StorageCapacity, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return StorageCapacity{}, err
	}
	var s unix.Statfs_t
	if err := unix.Statfs(path, &s); err != nil {
		return StorageCapacity{}, err
	}
	block := uint64(s.Bsize)
	reserved := uint64(0)
	if s.Bfree > s.Bavail {
		reserved = (s.Bfree - s.Bavail) * block
	}
	return StorageCapacity{Path: path, SampledAt: time.Now().UTC(), TotalBytes: s.Blocks * block, FreeBytes: s.Bfree * block, AvailableBytes: s.Bavail * block, ReservedBytes: reserved, InodesTotal: s.Files, InodesAvailable: s.Ffree}, nil
}
