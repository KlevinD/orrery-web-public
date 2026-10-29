package core

import "orrery_v2/internal/retrievalevidence"

type ToolID string
type SkillID string
type MemoryProfileID string
type MemoryDigestID string
type DiscussionDigestID string

type ToolRef struct {
	ToolID            ToolID
	VersionConstraint string
	Alias             string
	PermissionScope   string
	BindingPolicy     string
}

func (r ToolRef) IsZero() bool {
	return r.ToolID == ""
}

type MCPAuthorization struct {
	ServerToolID    ToolID
	HeaderName      string
	CredentialValue string
}

func (a MCPAuthorization) IsZero() bool {
	return a.ServerToolID == "" || a.CredentialValue == ""
}

type SkillRef struct {
	SkillID           SkillID
	VersionConstraint string
	Alias             string
	Level             string
	EvidenceRefs      []EvidenceRef
}

func (r SkillRef) IsZero() bool {
	return r.SkillID == ""
}

type MemoryKind string

const (
	MemoryKindPersonal     MemoryKind = "personal"
	MemoryKindCollective   MemoryKind = "collective"
	MemoryKindMission      MemoryKind = "mission"
	MemoryKindDiscussion   MemoryKind = "discussion"
	MemoryKindProject      MemoryKind = "project"
	MemoryKindOrganization MemoryKind = "organization"
)

func (k MemoryKind) IsValid() bool {
	switch k {
	case MemoryKindPersonal, MemoryKindCollective, MemoryKindMission, MemoryKindDiscussion, MemoryKindProject, MemoryKindOrganization:
		return true
	default:
		return false
	}
}

type MemoryProfileRef struct {
	MemoryProfileID   MemoryProfileID
	OwnerAgentID      AgentID
	MemoryKind        MemoryKind
	AccessScope       string
	RetrievalPolicy   string
	CompressionPolicy string
}

func (r MemoryProfileRef) IsZero() bool {
	return r.MemoryProfileID == ""
}

type MemoryDigestRef struct {
	Evidence    []retrievalevidence.Item `json:"evidence,omitempty"`
	ID          MemoryDigestID
	MemoryKind  MemoryKind
	SummaryRef  string
	Sensitivity string
}

func (r MemoryDigestRef) IsZero() bool {
	return r.ID == ""
}

type DiscussionDigestRef struct {
	ID         DiscussionDigestID
	SummaryRef string
}

type EvidenceRef struct {
	ID string
}
