package core

type MemoryLifecycleStatus string

const (
	MemoryLifecycleProposed   MemoryLifecycleStatus = "proposed"
	MemoryLifecycleActive     MemoryLifecycleStatus = "active"
	MemoryLifecycleSettled    MemoryLifecycleStatus = "settled"
	MemoryLifecycleDistilled  MemoryLifecycleStatus = "distilled"
	MemoryLifecycleArchived   MemoryLifecycleStatus = "archived"
	MemoryLifecycleDeleted    MemoryLifecycleStatus = "deleted"
	MemoryLifecycleAnonymized MemoryLifecycleStatus = "anonymized"
)

func (s MemoryLifecycleStatus) IsValid() bool {
	switch s {
	case MemoryLifecycleProposed,
		MemoryLifecycleActive,
		MemoryLifecycleSettled,
		MemoryLifecycleDistilled,
		MemoryLifecycleArchived,
		MemoryLifecycleDeleted,
		MemoryLifecycleAnonymized:
		return true
	default:
		return false
	}
}

func NormalizeMemoryLifecycleStatus(status MemoryLifecycleStatus) MemoryLifecycleStatus {
	if status.IsValid() && status != MemoryLifecycleProposed {
		return status
	}
	return MemoryLifecycleActive
}
