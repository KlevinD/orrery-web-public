package core

import "time"

type OrganizationID string
type DepartmentID string
type PositionID string
type RoleID string
type ReportingLineID string

type OrganizationRef struct {
	ID OrganizationID
}

type DepartmentRef struct {
	ID DepartmentID
}

type PositionRef struct {
	ID PositionID
}

type RoleRef struct {
	ID RoleID
}

type ReportingLineRef struct {
	ID ReportingLineID
}

type EmploymentType string

const (
	EmploymentTypeAIAgent EmploymentType = "ai_agent"
	EmploymentTypeHuman   EmploymentType = "human"
	EmploymentTypeSystem  EmploymentType = "system"
)

type AgentInstanceRef struct {
	ID AgentInstanceID
}

func (r AgentInstanceRef) IsZero() bool {
	return r.ID.IsZero()
}

type AgentProfileRef struct {
	ID AgentProfileID
}

func (r AgentProfileRef) IsZero() bool {
	return r.ID.IsZero()
}

type AgentOrgIdentity struct {
	OrganizationRef   OrganizationRef
	DepartmentRef     DepartmentRef
	PositionRef       PositionRef
	RoleRefs          []RoleRef
	ManagerAgentRef   AgentProfileRef
	ReportingLineRef  ReportingLineRef
	AuthorityScope    string
	EmploymentType    EmploymentType
	SupervisionPolicy SupervisionPolicy
}

type SupervisionPolicy string

const (
	SupervisionPolicyManaged    SupervisionPolicy = "managed"
	SupervisionPolicyRoot       SupervisionPolicy = "root"
	SupervisionPolicySupervised SupervisionPolicy = "supervised"
)

func (p SupervisionPolicy) AllowsMissingManager() bool {
	return p == SupervisionPolicyRoot || p == SupervisionPolicySupervised
}

type TalentPlacementStatus string

const (
	TalentPlacementInternal TalentPlacementStatus = "internal"
	TalentPlacementMarket   TalentPlacementStatus = "market"
)

func (s TalentPlacementStatus) IsValid() bool {
	switch s {
	case TalentPlacementInternal, TalentPlacementMarket:
		return true
	default:
		return false
	}
}

type TalentPlacementFilter string

const (
	TalentPlacementFilterInternal TalentPlacementFilter = "internal"
	TalentPlacementFilterMarket   TalentPlacementFilter = "market"
	TalentPlacementFilterAll      TalentPlacementFilter = "all"
)

type TalentPlacement struct {
	Status                     TalentPlacementStatus
	ReleasedFromOrganizationID OrganizationID
	ReleasedFromDepartmentID   DepartmentID
	ReleasedFromPositionID     PositionID
	ReleasedAt                 time.Time
	ReleasedReason             string
}

func (p TalentPlacement) NormalizedStatus(AgentOrgIdentity) TalentPlacementStatus {
	if p.Status.IsValid() {
		return p.Status
	}
	return TalentPlacementInternal
}

func (p TalentPlacement) IsInternal(org AgentOrgIdentity) bool {
	return p.NormalizedStatus(org) == TalentPlacementInternal
}

func (p TalentPlacement) IsMarket(org AgentOrgIdentity) bool {
	return p.NormalizedStatus(org) == TalentPlacementMarket
}

type AgentIdentity struct {
	AgentID        AgentID
	AgentProfileID AgentProfileID
	InstanceRef    AgentInstanceRef
}

func (i AgentIdentity) IsZero() bool {
	return i.AgentID.IsZero() && i.AgentProfileID.IsZero() && i.InstanceRef.IsZero()
}
