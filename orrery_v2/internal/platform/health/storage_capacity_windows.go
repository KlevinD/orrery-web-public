package health

import (
	"golang.org/x/sys/windows"
	"path/filepath"
	"time"
)

func ReadStorageCapacity(path string) (StorageCapacity, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return StorageCapacity{}, err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return StorageCapacity{}, err
	}
	s := StorageCapacity{Path: path, SampledAt: time.Now().UTC()}
	if err := windows.GetDiskFreeSpaceEx(p, &s.AvailableBytes, &s.TotalBytes, &s.FreeBytes); err != nil {
		return StorageCapacity{}, err
	}
	if s.FreeBytes > s.AvailableBytes {
		s.ReservedBytes = s.FreeBytes - s.AvailableBytes
	}
	return s, nil
}
