package team

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

type StaffingRequestID string
type StaffingRequestStatus string

const (
	StaffingRequestStatusRequested  StaffingRequestStatus = "requested"
	StaffingRequestStatusEvaluating StaffingRequestStatus = "evaluating"
	StaffingRequestStatusAppointed  StaffingRequestStatus = "appointed"
	StaffingRequestStatusRejected   StaffingRequestStatus = "rejected"
	StaffingRequestStatusCancelled  StaffingRequestStatus = "cancelled"
	StaffingRequestStatusExpired    StaffingRequestStatus = "expired"
)

type AvailabilityWindow struct {
	From  time.Time
	Until time.Time
}

type BudgetConstraints struct {
	MaxCostPerHour float64
	MaxCostPerTask float64
	BudgetClass    string
}

type QualityConstraints struct {
	MinScore      float64
	MinSampleSize int
}

type RiskConstraints struct {
	AllowedLevels     []agentcore.RiskLevel
	RequireMitigation bool
}

type StaffingRequest struct {
	ID                       StaffingRequestID
	MissionID                missioncore.MissionID
	RequestedBy              ActorRef
	CapabilityGap            string
	DesiredRoles             []MissionRole
	RequiredSkills           []agentcore.SkillRef
	RequiredTools            []agentcore.ToolRef
	AuthorityRequirements    string
	AvailabilityWindow       AvailabilityWindow
	BudgetConstraints        BudgetConstraints
	QualityConstraints       QualityConstraints
	RiskConstraints          RiskConstraints
	AllowUnknownAvailability bool
	AllowHuman               bool
	AllowAI                  bool
	Reason                   string
	Status                   StaffingRequestStatus
	CreatedAt                time.Time
	IdempotencyKey           string
}
