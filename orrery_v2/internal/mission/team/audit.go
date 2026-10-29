package team

import (
	"time"

	missioncore "orrery_v2/internal/mission/core"
)

type StaffingAuditID string

type StaffingAudit struct {
	ID                  StaffingAuditID
	MissionID           missioncore.MissionID
	RequestID           StaffingRequestID
	DecisionID          StaffingDecisionID
	Action              string
	SelectedRationale   string
	RejectedEvaluations []CandidateEvaluation
	CreatedAt           time.Time
	Metadata            map[string]string
}
