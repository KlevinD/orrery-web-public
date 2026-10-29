package team

type MissionRole string

const (
	MissionRoleLead                 MissionRole = "Lead"
	MissionRoleStaffing             MissionRole = "Staffing"
	MissionRolePlanner              MissionRole = "Planner"
	MissionRoleCoordinator          MissionRole = "Coordinator"
	MissionRoleWorker               MissionRole = "Worker"
	MissionRoleQAPlan               MissionRole = "QAPlan"
	MissionRoleQACheck              MissionRole = "QACheck"
	MissionRoleReviewer             MissionRole = "Reviewer"
	MissionRoleApprover             MissionRole = "Approver"
	MissionRoleRealHumanContributor MissionRole = "RealHumanContributor"
	MissionRoleObserver             MissionRole = "Observer"
)

func (r MissionRole) IsValid() bool {
	switch r {
	case MissionRoleLead, MissionRoleStaffing, MissionRolePlanner, MissionRoleCoordinator, MissionRoleWorker,
		MissionRoleQAPlan, MissionRoleQACheck, MissionRoleReviewer, MissionRoleApprover,
		MissionRoleRealHumanContributor, MissionRoleObserver:
		return true
	default:
		return false
	}
}

type ActorKind string

const (
	ActorKindAgent  ActorKind = "agent"
	ActorKindHuman  ActorKind = "human"
	ActorKindSystem ActorKind = "system"
)

type ActorRef struct {
	ID   string
	Kind ActorKind
}

func (r ActorRef) IsZero() bool {
	return r.ID == "" || r.Kind == ""
}
