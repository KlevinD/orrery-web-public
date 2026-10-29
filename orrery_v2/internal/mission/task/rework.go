package task

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type ReworkRequestID string
type ReworkStatus string

const (
	ReworkStatusOpen       ReworkStatus = "open"
	ReworkStatusAssigned   ReworkStatus = "assigned"
	ReworkStatusResolved   ReworkStatus = "resolved"
	ReworkStatusSuperseded ReworkStatus = "superseded"
	ReworkStatusCancelled  ReworkStatus = "cancelled"
)

type ReworkRequest struct {
	ID              ReworkRequestID
	OriginalTaskID  TaskID
	TargetAssignee  TaskAssigneeRef
	Reason          string
	FailedCriteria  []AcceptanceCriterionID
	MissingEvidence []EvidenceRequirementID
	RequestedBy     agentcore.AgentID
	CreatedAt       time.Time
	Status          ReworkStatus
}

type ReworkRequestOptions struct {
	ID              ReworkRequestID
	OriginalTaskID  TaskID
	TargetAssignee  TaskAssigneeRef
	Reason          string
	FailedCriteria  []AcceptanceCriterionID
	MissingEvidence []EvidenceRequirementID
	RequestedBy     agentcore.AgentID
	CreatedAt       time.Time
}

func NewReworkRequest(opts ReworkRequestOptions) ReworkRequest {
	createdAt := opts.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return ReworkRequest{
		ID:              opts.ID,
		OriginalTaskID:  opts.OriginalTaskID,
		TargetAssignee:  opts.TargetAssignee,
		Reason:          opts.Reason,
		FailedCriteria:  append([]AcceptanceCriterionID(nil), opts.FailedCriteria...),
		MissingEvidence: append([]EvidenceRequirementID(nil), opts.MissingEvidence...),
		RequestedBy:     opts.RequestedBy,
		CreatedAt:       createdAt.UTC(),
		Status:          ReworkStatusOpen,
	}
}
