package discussion

import (
	"time"

	missioncore "orrery_v2/internal/mission/core"
)

type DiscussionGroupID string
type DiscussionGroupStatus string

const (
	DiscussionGroupActive   DiscussionGroupStatus = "active"
	DiscussionGroupArchived DiscussionGroupStatus = "archived"
)

type DiscussionGroup struct {
	ID        DiscussionGroupID
	TenantID  string
	MissionID missioncore.MissionID
	Goal      string
	Status    DiscussionGroupStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}
