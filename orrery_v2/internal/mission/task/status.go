package task

type TaskStatus string

const (
	TaskStatusDraft           TaskStatus = "draft"
	TaskStatusAssigned        TaskStatus = "assigned"
	TaskStatusAccepted        TaskStatus = "accepted"
	TaskStatusInProgress      TaskStatus = "in_progress"
	TaskStatusBlocked         TaskStatus = "blocked"
	TaskStatusSubmitted       TaskStatus = "submitted"
	TaskStatusInReview        TaskStatus = "in_review"
	TaskStatusApproved        TaskStatus = "approved"
	TaskStatusRejected        TaskStatus = "rejected"
	TaskStatusReworkRequested TaskStatus = "rework_requested"
	TaskStatusCancelled       TaskStatus = "cancelled"
)

func (s TaskStatus) IsValid() bool {
	switch s {
	case TaskStatusDraft,
		TaskStatusAssigned,
		TaskStatusAccepted,
		TaskStatusInProgress,
		TaskStatusBlocked,
		TaskStatusSubmitted,
		TaskStatusInReview,
		TaskStatusApproved,
		TaskStatusRejected,
		TaskStatusReworkRequested,
		TaskStatusCancelled:
		return true
	default:
		return false
	}
}

func (s TaskStatus) IsTerminal() bool {
	return s == TaskStatusApproved || s == TaskStatusCancelled
}

func CanTransition(from, to TaskStatus) bool {
	if !from.IsValid() || !to.IsValid() || from.IsTerminal() {
		return false
	}
	allowed := map[TaskStatus][]TaskStatus{
		TaskStatusDraft:           {TaskStatusAssigned},
		TaskStatusAssigned:        {TaskStatusAccepted, TaskStatusCancelled},
		TaskStatusAccepted:        {TaskStatusInProgress, TaskStatusBlocked, TaskStatusCancelled},
		TaskStatusInProgress:      {TaskStatusSubmitted, TaskStatusBlocked, TaskStatusCancelled},
		TaskStatusBlocked:         {TaskStatusInProgress, TaskStatusReworkRequested, TaskStatusCancelled},
		TaskStatusSubmitted:       {TaskStatusInReview, TaskStatusApproved},
		TaskStatusInReview:        {TaskStatusApproved, TaskStatusRejected},
		TaskStatusRejected:        {TaskStatusReworkRequested},
		TaskStatusReworkRequested: {TaskStatusAssigned, TaskStatusAccepted, TaskStatusCancelled},
	}
	for _, candidate := range allowed[from] {
		if candidate == to {
			return true
		}
	}
	return false
}
