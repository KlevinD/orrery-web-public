//go:build !linux && !windows

package health

import "fmt"

func ReadStorageCapacity(path string) (StorageCapacity, error) {
	return StorageCapacity{}, fmt.Errorf("storage capacity sampling is unavailable on this platform")
}
