package discussion

import (
	"time"

	missioncore "orrery_v2/internal/mission/core"
)

type InboxItemID string
type InboxStatus string

const (
	InboxPending   InboxStatus = "pending"
	InboxRead      InboxStatus = "read"
	InboxResponded InboxStatus = "responded"
	InboxExpired   InboxStatus = "expired"
	InboxDismissed InboxStatus = "dismissed"
	InboxCancelled InboxStatus = "cancelled"
)

type InboxItem struct {
	ID                   InboxItemID
	TenantID             string
	MissionID            missioncore.MissionID
	DiscussionGroupID    DiscussionGroupID
	MessageID            MessageID
	MentionID            MentionID
	Recipient            ViewerRef
	RequiredResponse     bool
	Status               InboxStatus
	DueAt                time.Time
	RespondedByMessageID MessageID
	NotificationState    NotificationState
	CreatedAt            time.Time
	ClosedAt             time.Time
}

type InboxQuery struct {
	MissionID     missioncore.MissionID
	IncludeClosed bool
	Limit         int
}

type InboxPage struct {
	Items []InboxItem
}

func (s InboxStatus) Closed() bool {
	switch s {
	case InboxResponded, InboxExpired, InboxDismissed, InboxCancelled:
		return true
	default:
		return false
	}
}
