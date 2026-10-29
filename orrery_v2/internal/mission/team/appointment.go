package team

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

type AppointmentID string

type Appointment struct {
	ID              AppointmentID
	MissionID       missioncore.MissionID
	MemberID        MissionTeamMemberID
	AssignmentID    MissionAssignmentID
	AgentProfileID  agentcore.AgentProfileID
	AgentInstanceID agentcore.AgentInstanceID
	Role            MissionRole
	AppointedBy     ActorRef
	AppointedAt     time.Time
}

type AppointmentCommand struct {
	IDempotencyKey                string
	RequestID                     StaffingRequestID
	MissionID                     missioncore.MissionID
	ProfileID                     agentcore.AgentProfileID
	Role                          MissionRole
	Responsibility                string
	AuthorityScope                string
	RequiredSkills                []agentcore.SkillRef
	RequiredTools                 []agentcore.ToolRef
	AvailabilityWindow            AvailabilityWindow
	BudgetConstraints             BudgetConstraints
	QualityConstraints            QualityConstraints
	RiskConstraints               RiskConstraints
	AllowUnknownAvailability      bool
	AllowHuman                    bool
	AllowAI                       bool
	AllowOrganizationalRoleBridge bool
	AppointedBy                   ActorRef
	Reason                        string
}
