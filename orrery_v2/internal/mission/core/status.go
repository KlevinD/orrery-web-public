package core

type MissionStatus string

const (
	MissionStatusDraft     MissionStatus = "draft"
	MissionStatusActive    MissionStatus = "active"
	MissionStatusPaused    MissionStatus = "paused"
	MissionStatusCompleted MissionStatus = "completed"
	MissionStatusCancelled MissionStatus = "cancelled"
)

func (s MissionStatus) IsValid() bool {
	switch s {
	case MissionStatusDraft, MissionStatusActive, MissionStatusPaused, MissionStatusCompleted, MissionStatusCancelled:
		return true
	default:
		return false
	}
}
