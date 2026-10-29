package core

import "time"

type AvailabilityStatus string

const (
	AvailabilityAvailable AvailabilityStatus = "available"
	AvailabilityBusy      AvailabilityStatus = "busy"
	AvailabilityOffline   AvailabilityStatus = "offline"
	AvailabilityDisabled  AvailabilityStatus = "disabled"
	AvailabilityUnknown   AvailabilityStatus = "unknown"
)

func (s AvailabilityStatus) IsAvailable() bool {
	return s == AvailabilityAvailable
}

type AgentAvailability struct {
	AgentProfileID  AgentProfileID
	AgentInstanceID AgentInstanceID
	Status          AvailabilityStatus
	AvailableFrom   time.Time
	AvailableUntil  time.Time
	Capacity        int
	CurrentLoad     int
	Reason          string
	Source          string
	UpdatedAt       time.Time
}
