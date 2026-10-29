package health

import "time"

// StorageCapacity reports the space available to this service account, together
// with all free blocks. Reserved blocks are not application write capacity.
type StorageCapacity struct {
	Path            string    `json:"path"`
	SampledAt       time.Time `json:"sampled_at"`
	TotalBytes      uint64    `json:"total_bytes"`
	FreeBytes       uint64    `json:"all_free_bytes"`
	AvailableBytes  uint64    `json:"available_bytes"`
	ReservedBytes   uint64    `json:"reserved_unavailable_bytes"`
	InodesTotal     uint64    `json:"inodes_total,omitempty"`
	InodesAvailable uint64    `json:"inodes_available,omitempty"`
}

func (s StorageCapacity) Low() bool {
	return s.AvailableBytes < 1<<30 || (s.TotalBytes > 0 && s.AvailableBytes < s.TotalBytes/20) || (s.InodesTotal > 0 && s.InodesAvailable < s.InodesTotal/100)
}

func (s StorageCapacity) Critical() bool {
	return s.AvailableBytes < 256<<20 || (s.InodesTotal > 0 && s.InodesAvailable == 0)
}
