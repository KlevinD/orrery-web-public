package core

import "time"

type AgentDirectoryQuery struct {
	Organization              OrganizationID
	Department                DepartmentID
	Position                  PositionID
	Roles                     []RoleID
	Manager                   AgentProfileID
	ReportingLine             ReportingLineID
	AgentKind                 AgentKind
	EmploymentType            EmploymentType
	SkillRefs                 []SkillRef
	ToolRefs                  []ToolRef
	RuntimeKind               RuntimeKind
	MemoryKind                MemoryKind
	AvailabilityWindow        QueryAvailabilityWindow
	AuthorityScope            string
	Placement                 TalentPlacementFilter
	MaxCost                   float64
	MinQualityScore           float64
	AllowedRiskLevels         []RiskLevel
	EnabledOnly               bool
	ExcludeAgentProfiles      []AgentProfileID
	IncludeHumans             bool
	IncludeAI                 bool
	IncludePersonalAssistants bool
	Page                      Page
	Sort                      DirectorySort
}

type QueryAvailabilityWindow struct {
	From  time.Time
	Until time.Time
}

type DirectorySort string

const (
	DirectorySortScoreAsc  DirectorySort = "score_asc"
	DirectorySortScoreDesc DirectorySort = "score_desc"
	DirectorySortUpdated   DirectorySort = "updated"
)

type Page struct {
	Limit  int
	Offset int
}

type AgentDirectoryPage struct {
	Entries  []AgentDirectoryEntry
	Rejected []DirectoryRejection
	Total    int
	Limit    int
	Offset   int
}

type DirectoryRejection struct {
	AgentProfileID  AgentProfileID
	AgentInstanceID AgentInstanceID
	Code            string
	Reason          string
}
