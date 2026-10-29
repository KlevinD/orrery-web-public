package agentrunrecovery

import "orrery_v2/internal/agent/runtime/agentkernel"

type View struct {
	agentkernel.TaskInspection
	CanReview  bool `json:"can_review"`
	CanRestart bool `json:"can_restart"`
}

type ReviewInput = agentkernel.TaskReviewInput
type Review = agentkernel.TaskReview
