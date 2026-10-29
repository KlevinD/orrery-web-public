package team

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type StaffingDecisionID string

type StaffingDecision struct {
	ID                      StaffingDecisionID
	RequestID               StaffingRequestID
	SelectedAgentProfileID  agentcore.AgentProfileID
	SelectedAgentInstanceID agentcore.AgentInstanceID
	Appointment             Appointment
	RejectedCandidates      []CandidateEvaluation
	Rationale               string
	DecidedBy               ActorRef
	DecidedAt               time.Time
	AuditRef                StaffingAuditID
	IdempotencyKey          string
}
